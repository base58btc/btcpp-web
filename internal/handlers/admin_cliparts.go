package handlers

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/imgproc"

	"github.com/gorilla/mux"
)

// AdminCliparts renders /{conf}/admin/cliparts — a per-talk table
// for uploading clipart images. Each row's filename input pre-fills
// with `{conftag}_<keyword>` derived from the talk's title; admins
// can edit before submitting. Saves PNG + AVIF to talks/<filename>
// in Spaces, updates the talks/_manifest.json hash index, and
// patches ConfTalk.Clipart in Notion.
func AdminCliparts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	// Iterate Proposals (filtered to talks that should have art
	// queued up), not the derived talks slice — ConfTalks are
	// lazy-created when an admin first places a talk on the
	// schedule grid, so an Accepted talk that hasn't been scheduled
	// yet has no ConfTalk row and would silently disappear from a
	// talks-slice-based listing. The upload handler lazy-creates
	// the ConfTalk on first save.
	//
	// Inclusion rules:
	//   - Accepted / Scheduled: always — these are committed talks.
	//   - Invited: only when the ConfTalk exists AND has a TalkTime
	//     (Sched != nil). Lets admins pre-stage artwork for a
	//     pencilled-in invitee they expect to confirm, without
	//     flooding the page with every outstanding invite.
	proposals := loadConfProposals(ctx, conf)
	rows := make([]*ClipartRow, 0, len(proposals))
	for _, p := range proposals {
		if p == nil {
			continue
		}
		ct := getters.FetchConfTalkByProposal(p.ID)
		switch p.Status {
		case StatusAccepted, StatusScheduled:
			// always include — ConfTalk may not exist yet
		case "Invited":
			if ct == nil || ct.Sched == nil {
				continue
			}
		default:
			continue
		}
		row := &ClipartRow{
			ProposalID: p.ID,
			TalkTitle:  p.Title,
			Status:     p.Status,
		}
		if ct != nil {
			row.CurrentClipart = ct.Clipart
			if ct.Clipart != "" {
				row.ClipartURL = spaces.PublicURL("talks/" + ct.Clipart)
			}
		}
		// Pre-fill the filename input with the existing clipart
		// name (sans extension) so re-uploads default to
		// overwriting the same Spaces key + Notion field. New
		// rows fall through to the suggester, which now bakes
		// 2 hex bytes of proposal-ID uniqueness into the name
		// to avoid two talks sharing a suggestion.
		if row.CurrentClipart != "" {
			row.SuggestedName = strings.TrimSuffix(row.CurrentClipart, filepath.Ext(row.CurrentClipart))
		} else {
			row.SuggestedName = suggestClipartName(conf.Tag, p.Title, p.ID)
		}
		rows = append(rows, row)
	}
	// Sort: missing-clipart rows first, then by talk title for
	// stable rendering. Surfaces what needs work at the top.
	sort.SliceStable(rows, func(i, j int) bool {
		ai := rows[i].CurrentClipart == ""
		aj := rows[j].CurrentClipart == ""
		if ai != aj {
			return ai
		}
		return strings.ToLower(rows[i].TalkTitle) < strings.ToLower(rows[j].TalkTitle)
	})

	page := &AdminClipartsPage{
		Conf:         conf,
		Rows:         rows,
		FlashMessage: r.URL.Query().Get("flash"),
		ErrorMessage: r.URL.Query().Get("error"),
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/cliparts.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/cliparts render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// AdminClipartsUpload handles the per-talk multipart upload form on
// /admin/cliparts. Pipeline:
//
//  1. Parse multipart body (cap 10 MB).
//  2. Sanitize filename → "<safe>.png", reject anything weird.
//  3. Read image bytes from the form.
//  4. Generate AVIF via ffmpeg (preserves aspect — talk cliparts
//     aren't all square).
//  5. Upload PNG + AVIF to talks/<name>.{png,avif} in Spaces.
//  6. Load → mutate → save talks/_manifest.json with content hashes.
//  7. Patch Notion ConfTalk.Clipart = "<name>.png".
//  8. Invalidate the in-memory talk-manifest cache so card-hash
//     fingerprints pick up the new entry immediately (vs waiting on
//     the 5-min TTL).
//
// Failures redirect back to the cliparts page with ?error=… and the
// admin can retry. PNG-only inputs are required (we don't accept
// JPEG / WebP / etc. for now — keeps the manifest filename
// extension story simple, and ffmpeg picks up the format anyway so
// pasted PNG screenshots Just Work).
func AdminClipartsUpload(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	proposalID := mux.Vars(r)["proposalID"]
	bail := func(msg string) {
		ctx.Err.Printf("/%s/admin/cliparts/%s upload: %s", conf.Tag, proposalID, msg)
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/cliparts?error=%s", conf.Tag, url.QueryEscape(msg)),
			http.StatusSeeOther)
	}
	if proposalID == "" {
		bail("Missing proposal ID.")
		return
	}

	// Resolve (or lazy-create) the ConfTalk for this proposal. The
	// schedule grid normally creates one on first place; the cliparts
	// page is the only flow where admins might assign artwork before
	// scheduling, so accept the cost of a CreateConfTalk here when
	// the row doesn't exist yet.
	confTalkID := ""
	if ct := getters.FetchConfTalkByProposal(proposalID); ct != nil {
		confTalkID = ct.ID
	}
	if confTalkID == "" {
		newID, err := getters.CreateConfTalk(ctx, getters.ConfTalkInput{
			ConfTag:    conf.Tag,
			ProposalID: proposalID,
		})
		if err != nil {
			bail("Couldn't create ConfTalk: " + err.Error())
			return
		}
		confTalkID = newID
		getters.InvalidateConfTalksCache()
	}

	limitRequestBody(w, r, maxMultipartBodyBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		bail("Form parse failed: " + err.Error())
		return
	}

	rawName := strings.TrimSpace(r.FormValue("filename"))
	clean := sanitizeClipartName(rawName)
	if clean == "" {
		bail("Filename required (alphanumeric / underscore / hyphen).")
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		bail("No image attached: " + err.Error())
		return
	}
	defer file.Close()
	pngBytes, err := io.ReadAll(file)
	if err != nil {
		bail("Read image: " + err.Error())
		return
	}
	if len(pngBytes) == 0 {
		bail("Empty image upload.")
		return
	}

	avifBytes, err := imgproc.MakeAVIF(pngBytes, 0)
	if err != nil {
		bail("AVIF transcode failed: " + err.Error())
		return
	}

	pngKey := "talks/" + clean + ".png"
	avifKey := "talks/" + clean + ".avif"
	if _, err := spaces.Upload(pngKey, pngBytes, "image/png", ""); err != nil {
		bail("Upload PNG: " + err.Error())
		return
	}
	if _, err := spaces.Upload(avifKey, avifBytes, "image/avif", ""); err != nil {
		bail("Upload AVIF: " + err.Error())
		return
	}

	manifest, err := spaces.LoadJSONMap(spaces.TalkManifestKey)
	if err != nil {
		ctx.Err.Printf("/%s/admin/cliparts load manifest (continuing): %s", conf.Tag, err)
		manifest = map[string]string{}
	}
	manifest[clean+".png"] = contentHashShort(pngBytes)
	manifest[clean+".avif"] = contentHashShort(avifBytes)
	if err := spaces.SaveJSONMap(spaces.TalkManifestKey, manifest); err != nil {
		ctx.Err.Printf("/%s/admin/cliparts save manifest (continuing): %s", conf.Tag, err)
	}
	InvalidateTalkManifest()

	if err := getters.ConfTalkSetClipart(ctx, confTalkID, clean+".png"); err != nil {
		bail("Patch Notion ConfTalk.Clipart: " + err.Error())
		return
	}
	// Bust both caches: the underlying ConfTalk index AND the
	// derived legacy talks slice that GetTalksFor reads from on
	// the cliparts GET. Without the second one, the redirect
	// would show the stale clipart from before the upload.
	getters.InvalidateConfTalksCache()
	getters.InvalidateTalksCache()

	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/cliparts?flash=%s",
			conf.Tag, url.QueryEscape("Uploaded "+clean+".png + .avif")),
		http.StatusSeeOther)
}

// sanitizeClipartName trims a user-supplied filename to alphanumerics,
// underscores, and hyphens and lowercases the result. Strips any
// extension the caller threw in (handler always appends .png / .avif).
// Returns "" if the cleaned value is empty — caller bails to the
// error redirect.
var clipartNameStrip = regexp.MustCompile(`[^a-z0-9_\-]+`)

func sanitizeClipartName(raw string) string {
	noExt := strings.TrimSuffix(raw, filepath.Ext(raw))
	lower := strings.ToLower(noExt)
	clean := clipartNameStrip.ReplaceAllString(lower, "")
	clean = strings.Trim(clean, "_-")
	return clean
}

// suggestClipartName picks a default filename for a talk: the
// conf-tag prefix + the first non-trivial word of the talk title +
// 4 hex chars derived from the proposal ID. The hex suffix is
// deterministic (sha1 of the proposal ID, truncated) so repeated
// renders of the page suggest the same name, but distinct across
// talks even when titles collide (a "Closing keynote" panel at two
// confs would otherwise both suggest "{tag}_closing"). The
// suggestion is just a hint — admins can override in the form's
// filename input.
func suggestClipartName(confTag, title, proposalID string) string {
	base := confTag + "_clipart"
	clean := strings.ToLower(strings.TrimSpace(title))
	clean = clipartNameStrip.ReplaceAllString(clean, " ")
	for _, w := range strings.Fields(clean) {
		if len(w) >= 4 && !clipartStopword(w) {
			base = confTag + "_" + w
			break
		}
	}
	if base == confTag+"_clipart" {
		for _, w := range strings.Fields(clean) {
			if len(w) >= 3 {
				base = confTag + "_" + w
				break
			}
		}
	}
	if proposalID == "" {
		return base
	}
	h := sha1.Sum([]byte(proposalID))
	return base + "_" + hex.EncodeToString(h[:2])
}

// clipartStopword filters words that aren't useful as filename
// keywords ("the", "and", …). Not exhaustive; covers the common
// run that appears at the front of talk titles.
func clipartStopword(w string) bool {
	switch w {
	case "the", "and", "for", "with", "from", "into", "your", "what",
		"that", "this", "you", "its", "are", "but", "how", "why",
		"when", "where", "intro", "introducing":
		return true
	}
	return false
}

// contentHashShort returns the first 16 hex chars of sha256(data) —
// matches the upload-talk-cliparts CLI shape so manifest entries
// produced by either path are interchangeable.
func contentHashShort(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])[:16]
}
