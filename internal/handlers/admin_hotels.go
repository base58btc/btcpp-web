package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/auth"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/types"
)

// HotelManagerSlots is the maximum number of hotels surfaced on the
// admin editor (and therefore the maximum that fit on a conf page's
// "Where to Stay" grid). Empty rows render as blank slots so the
// admin can fill in a fresh hotel without a separate "Add" click.
const HotelManagerSlots = 4

// HotelAdminPage drives /{conf}/admin/hotels — the four-row editor
// for a single conf's hotels.
type HotelAdminPage struct {
	Conf         *types.Conf
	Slots        []*HotelSlot
	FlashMessage string
	FlashError   string
	// IsConfAdmin gates the edit form. Staff get a read-only view
	// of the same four rows; the Save / Upload / Delete controls
	// only render for admins (and the POST handlers gate too).
	IsConfAdmin bool
	Year        uint
}

// HotelSlot is one row in the editor. Empty slots have ID == "" so
// the form's POST handler knows to CreateHotel rather than Update.
// SuggestedOrder pre-fills the Order input with a sensible default
// for new rows: existing slot's Order, or the row index for blanks.
type HotelSlot struct {
	Index          int
	Hotel          *types.Hotel
	SuggestedOrder int
}

// HotelsAdmin renders the manager page. Read access only — saves +
// image uploads stay behind requireConfAdmin (HotelsAdminSave,
// HotelImageUpload), so staff can view the booking matrix without
// being able to overwrite it.
func HotelsAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	id := requireConfStaff(w, r, ctx)
	if id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	hotels := helpers.HotelsForConf(ctx, conf)
	page := &HotelAdminPage{
		Conf:         conf,
		Slots:        buildHotelSlots(hotels),
		FlashMessage: r.URL.Query().Get("flash"),
		FlashError:   r.URL.Query().Get("error"),
		IsConfAdmin:  id.HasRoleForConf(conf.Tag, auth.RoleAdmin),
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/hotels.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/hotels render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// HotelsAdminSave processes the multi-row POST. Each row carries a
// HotelID (empty for new), a Delete checkbox (skipped on new rows),
// and the editable fields. The handler walks all four rows and
// dispatches Create / Update / Archive per row, then redirects back
// with a green flash summarizing what changed.
func HotelsAdminSave(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	var created, updated, archived int
	var firstErr error
	for i := 0; i < HotelManagerSlots; i++ {
		prefix := fmt.Sprintf("h%d_", i)
		hotelID := strings.TrimSpace(r.FormValue(prefix + "ID"))
		deleteRow := r.FormValue(prefix+"Delete") == "1"
		name := strings.TrimSpace(r.FormValue(prefix + "Name"))
		hotelURL := strings.TrimSpace(r.FormValue(prefix + "URL"))
		img := strings.TrimSpace(r.FormValue(prefix + "Img"))
		typ := strings.TrimSpace(r.FormValue(prefix + "Type"))
		desc := strings.TrimSpace(r.FormValue(prefix + "Desc"))
		order, _ := strconv.Atoi(r.FormValue(prefix + "Order"))

		switch {
		case hotelID != "" && deleteRow:
			if err := getters.ArchiveHotel(ctx, hotelID); err != nil {
				ctx.Err.Printf("/%s/admin/hotels archive %s: %s", conf.Tag, hotelID, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			archived++

		case hotelID != "":
			// Update an existing hotel. Skip rows the admin
			// hasn't actually changed (no name + no img + no
			// url) — but Order is always saved since "0" is a
			// legitimate value an admin might be moving toward.
			err := getters.UpdateHotel(ctx, hotelID, getters.HotelInput{
				Name:  name,
				URL:   hotelURL,
				Img:   img,
				Type:  typ,
				Desc:  desc,
				Order: order,
			})
			if err != nil {
				ctx.Err.Printf("/%s/admin/hotels update %s: %s", conf.Tag, hotelID, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			updated++

		case name != "" || img != "" || hotelURL != "":
			// New row — create only when at least one
			// non-Order field is filled. Pure-blank rows are
			// no-ops.
			_, err := getters.CreateHotel(ctx, getters.HotelInput{
				Name:    name,
				URL:     hotelURL,
				Img:     img,
				Type:    typ,
				Desc:    desc,
				Order:   order,
				ConfRef: conf.Ref,
			})
			if err != nil {
				ctx.Err.Printf("/%s/admin/hotels create: %s", conf.Tag, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			created++
		}
	}

	getters.RefreshHotelsCache()

	dest := fmt.Sprintf("/%s/admin/hotels", conf.Tag)
	if firstErr != nil {
		http.Redirect(w, r,
			dest+"?error="+url.QueryEscape("Save partially failed: "+firstErr.Error()),
			http.StatusSeeOther)
		return
	}
	flash := fmt.Sprintf("Saved — %d new, %d updated, %d removed.", created, updated, archived)
	http.Redirect(w, r, dest+"?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}

// HotelImageUpload mirrors a hotel image to Spaces under
// {conftag}/hotels/{shortID}{ext} and returns the bare path as JSON
// so the form's per-row Upload button can drop it into the hidden
// Img input. Idempotent on identical content via spaces.Exists.
//
// Same shape as OrgLogoUpload; per-conf scope via {conf} mux var.
func HotelImageUpload(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	limitRequestBody(w, r, maxMultipartBodyBytes)
	raw, contentType, ext, err := readMultipartFile(r, "file")
	if err != nil {
		http.Error(w, "missing or unreadable file", http.StatusBadRequest)
		return
	}
	if !spaces.IsConfigured() {
		http.Error(w, "spaces not configured", http.StatusInternalServerError)
		return
	}
	shortID := imgproc.ShortID(raw)
	key := conf.Tag + "/hotels/" + shortID + ext
	if !spaces.Exists(key) {
		if _, err := spaces.Upload(key, raw, contentType, ""); err != nil {
			ctx.Err.Printf("/%s/admin/hotels/upload-img: %s", conf.Tag, err)
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"path": key,
		"url":  spaces.PublicURL(key),
	})
}

// buildHotelSlots projects the conf's existing hotels into a fixed-
// length slice of HotelManagerSlots entries. Existing hotels fill
// the first N slots in their HotelsForConf-sorted order; any
// remaining slots are blank rows ready for new entries. Suggested
// orders start at the highest existing Order + 10 (or 10/20/30/40
// when there are no hotels yet) so a freshly typed row lands at the
// end by default and the admin can re-rank by editing the number.
func buildHotelSlots(existing []*types.Hotel) []*HotelSlot {
	slots := make([]*HotelSlot, HotelManagerSlots)
	// Defensive sort — HotelsForConf already sorts by Order, but
	// belt-and-suspenders here so a future caller can't break the
	// editor by passing an unsorted slice.
	sortable := append([]*types.Hotel(nil), existing...)
	sort.SliceStable(sortable, func(i, j int) bool {
		return sortable[i].Order < sortable[j].Order
	})
	maxOrder := 0
	for _, h := range sortable {
		if h.Order > maxOrder {
			maxOrder = h.Order
		}
	}
	for i := 0; i < HotelManagerSlots; i++ {
		slot := &HotelSlot{Index: i}
		if i < len(sortable) {
			slot.Hotel = sortable[i]
			slot.SuggestedOrder = sortable[i].Order
		} else {
			// Blank row — suggest a value past the highest
			// existing Order so the new hotel slots in at
			// the end. Step by 10 so the admin has room to
			// re-rank without renumbering everything.
			slot.SuggestedOrder = maxOrder + 10*(i-len(sortable)+1)
		}
		slots[i] = slot
	}
	return slots
}
