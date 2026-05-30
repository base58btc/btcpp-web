package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/missives"
	"btcpp-web/internal/types"
)

// compTicketTypes is the closed list of ticket-Type values the admin
// form accepts. Mirrors the values the regular checkout / speaker /
// volunteer flows write — keeps the PurchasesDb Type column from
// growing fresh options that don't match anything elsewhere.
var compTicketTypes = []string{
	"genpop",
	"local",
	"speaker",
	"sponsor",
	"volunteer",
}

const compTicketMaxCount = 25

// CompTicketsPage is the data shape rendered by the comp-tickets form.
// Loaded GET or after a failed POST so the user sees what they typed
// + the error / flash banner.
type CompTicketsPage struct {
	Conf     *types.Conf
	Types    []string // dropdown options, fixed list above
	Email    string   // last submitted email (for re-render after error)
	Count    int      // last submitted count
	Type     string   // last submitted ticket type
	Flash    string
	FlashErr string
	Year     uint
}

// AdminCompTickets renders the per-conf "issue complimentary tickets"
// form on GET and processes the form on POST. Admin-only — comp
// tickets bypass payment and arrive in attendee inboxes with PDFs
// attached, so we want a stricter gate than {conf}-staff.
//
// Path: GET /{conf}/admin/comp-tickets
//
//	POST /{conf}/admin/comp-tickets
func AdminCompTickets(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil || conf == nil {
		handle404(w, r, ctx)
		return
	}

	page := &CompTicketsPage{
		Conf:  conf,
		Types: compTicketTypes,
		Count: 1,
		Type:  "genpop",
		Flash: r.URL.Query().Get("flash"),
		Year:  helpers.CurrentYear(),
	}

	if r.Method == http.MethodPost {
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			page.FlashErr = "Couldn't read form. Try again."
			renderCompTicketsForm(w, r, ctx, page)
			return
		}
		page.Email = strings.TrimSpace(r.PostForm.Get("email"))
		page.Type = strings.TrimSpace(r.PostForm.Get("type"))
		countRaw := strings.TrimSpace(r.PostForm.Get("count"))
		n, errN := strconv.Atoi(countRaw)
		page.Count = n
		switch {
		case page.Email == "" || !strings.Contains(page.Email, "@"):
			page.FlashErr = "Need a valid email address."
		case errN != nil || n < 1:
			page.FlashErr = "Count must be a positive integer."
		case n > compTicketMaxCount:
			page.FlashErr = fmt.Sprintf("Max %d tickets per batch — issue another batch if you really need more.", compTicketMaxCount)
		case !isValidCompTicketType(page.Type):
			page.FlashErr = fmt.Sprintf("Unknown ticket type %q.", page.Type)
		}
		if page.FlashErr != "" {
			renderCompTicketsForm(w, r, ctx, page)
			return
		}

		if err := issueCompTickets(ctx, conf, page.Email, page.Type, n); err != nil {
			ctx.Err.Printf("/%s/admin/comp-tickets issue (email=%s type=%s n=%d): %s",
				conf.Tag, page.Email, page.Type, n, err)
			page.FlashErr = "Issuing tickets failed — see server logs."
			renderCompTicketsForm(w, r, ctx, page)
			return
		}

		flash := fmt.Sprintf("Issued %d %s ticket(s) to %s. The mailer's next tick will email the PDFs.",
			n, page.Type, page.Email)
		ctx.Infos.Printf("/%s/admin/comp-tickets ok: %s", conf.Tag, flash)
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/comp-tickets?flash=%s", conf.Tag, url.QueryEscape(flash)),
			http.StatusSeeOther)
		return
	}

	renderCompTicketsForm(w, r, ctx, page)
}

func renderCompTicketsForm(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, page *CompTicketsPage) {
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/comp_tickets.tmpl", page); err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/comp-tickets template: %s", page.Conf.Tag, err)
	}
}

// issueCompTickets writes `count` PurchasesDb rows for `email` at the
// given conf + ticket type. Each row gets its own UniqueID
// (sha256(email || batchID || index)) so the mailer treats them as
// distinct tickets and sends one email per ticket. Currency is USD
// + Amount Paid = 0 — the regular checkout writes the buyer-paid
// number, comp tickets are free.
//
// Mirrors the speaker-comp flow in accept_speaker.go:issueSpeakerTicket
// but with a fresh, random batch ID (so re-clicks issue more tickets
// rather than upserting). Subscribes the recipient to the conf's
// post-purchase newsletter, same as a paid checkout.
func issueCompTickets(ctx *config.AppContext, conf *types.Conf, email, tixType string, count int) error {
	batchID := freshCompBatchID()
	items := make([]types.Item, count)
	for i := range items {
		items[i] = types.Item{Total: 0, Desc: conf.Desc, Type: tixType}
	}
	entry := types.Entry{
		ID:       batchID,
		ConfRef:  conf.Ref,
		Currency: "USD",
		Created:  time.Now(),
		Email:    email,
		Items:    items,
	}
	if err := getters.AddTickets(ctx, &entry, "admincomp"); err != nil {
		return fmt.Errorf("AddTickets: %w", err)
	}
	if err := missives.NewTicketSub(ctx, email, conf.Tag, tixType, false); err != nil {
		// Newsletter sub failure shouldn't block the ticket-issue;
		// the rows are in PurchasesDb so the mailer will still
		// email them.
		ctx.Err.Printf("/%s/admin/comp-tickets newsletter sub for %s: %s", conf.Tag, email, err)
	}
	return nil
}

func isValidCompTicketType(s string) bool {
	for _, t := range compTicketTypes {
		if t == s {
			return true
		}
	}
	return false
}

// freshCompBatchID returns a unique entry-ID for one comp-ticket
// batch — combines wall-clock nanos with 8 random bytes so concurrent
// admin clicks never collide and re-clicks always produce a new
// batch (rather than UPSERT-merging into a previously-issued one).
func freshCompBatchID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("admincomp-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}
