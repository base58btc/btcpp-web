package handlers

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

type AdminDiscountsPage struct {
	Conf      *types.Conf
	Discounts []AdminDiscountRow
	Form      DiscountForm
	Flash     string
	FlashErr  string
	Year      uint
}

type AdminDiscountRow struct {
	ID             string
	CodeName       string
	Expression     string
	AmountLabel    string
	ValidDates     string
	UsesLabel      string
	AffiliateEmail string
	Form           DiscountForm
}

type DiscountForm struct {
	ID             string
	CodeName       string
	DiscountType   string
	Amount         string
	ValidFrom      string
	ExpiresAt      string
	MaxAllowed     string
	AffiliateEmail string
}

func AdminDiscounts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil || conf == nil {
		handle404(w, r, ctx)
		return
	}

	page := &AdminDiscountsPage{
		Conf:      conf,
		Discounts: discountsForConf(ctx, conf),
		Flash:     r.URL.Query().Get("flash"),
		Year:      helpers.CurrentYear(),
	}

	if r.Method == http.MethodPost {
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			page.FlashErr = "Couldn't read form. Try again."
			renderAdminDiscounts(w, r, ctx, page)
			return
		}
		action := strings.TrimSpace(r.PostForm.Get("action"))
		if action == "" {
			action = "create"
		}
		page.Form = discountFormFromRequest(r)
		if action == "delete" {
			if err := deleteAdminDiscount(ctx, conf, page.Form.ID); err != nil {
				ctx.Err.Printf("/%s/admin/discounts delete %s: %s", conf.Tag, page.Form.ID, err)
				page.FlashErr = err.Error()
				renderAdminDiscounts(w, r, ctx, page)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/discounts?flash=%s", conf.Tag, url.QueryEscape("Deleted discount code.")), http.StatusSeeOther)
			return
		}
		expr, err := buildDiscountExpr(page.Form)
		if err != nil {
			page.FlashErr = err.Error()
			renderAdminDiscounts(w, r, ctx, page)
			return
		}

		switch action {
		case "create":
			if available, err := getters.IsCodeNameAvailable(ctx, page.Form.CodeName); err != nil {
				ctx.Err.Printf("/%s/admin/discounts availability %s: %s", conf.Tag, page.Form.CodeName, err)
				page.FlashErr = "Couldn't check whether that code already exists."
				renderAdminDiscounts(w, r, ctx, page)
				return
			} else if !available {
				page.FlashErr = "That code already exists. Pick another code name."
				renderAdminDiscounts(w, r, ctx, page)
				return
			}

			_, err = getters.CreateDiscount(ctx, getters.DiscountInput{
				CodeName:       strings.ToUpper(page.Form.CodeName),
				DiscountExpr:   expr,
				ConfRef:        conf.Ref,
				AffiliateEmail: page.Form.AffiliateEmail,
			})
			if err != nil {
				ctx.Err.Printf("/%s/admin/discounts create %s (%s): %s", conf.Tag, page.Form.CodeName, expr, err)
				page.FlashErr = "Creating the discount failed. Check server logs."
				renderAdminDiscounts(w, r, ctx, page)
				return
			}

			flash := fmt.Sprintf("Created %s for %s.", strings.ToUpper(page.Form.CodeName), conf.Desc)
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/discounts?flash=%s", conf.Tag, url.QueryEscape(flash)), http.StatusSeeOther)
			return

		case "update":
			if err := updateAdminDiscount(ctx, conf, page.Form, expr); err != nil {
				ctx.Err.Printf("/%s/admin/discounts update %s (%s): %s", conf.Tag, page.Form.ID, expr, err)
				page.FlashErr = err.Error()
				renderAdminDiscounts(w, r, ctx, page)
				return
			}
			flash := fmt.Sprintf("Updated %s.", strings.ToUpper(page.Form.CodeName))
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/discounts?flash=%s", conf.Tag, url.QueryEscape(flash)), http.StatusSeeOther)
			return

		default:
			page.FlashErr = "Unknown discount action."
			renderAdminDiscounts(w, r, ctx, page)
			return
		}
	}

	renderAdminDiscounts(w, r, ctx, page)
}

func renderAdminDiscounts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, page *AdminDiscountsPage) {
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/discounts.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/discounts template: %s", page.Conf.Tag, err)
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
	}
}

func discountFormFromRequest(r *http.Request) DiscountForm {
	return DiscountForm{
		ID:             strings.TrimSpace(r.PostForm.Get("discount_id")),
		CodeName:       strings.TrimSpace(r.PostForm.Get("code_name")),
		DiscountType:   strings.TrimSpace(r.PostForm.Get("discount_type")),
		Amount:         strings.TrimSpace(r.PostForm.Get("amount")),
		ValidFrom:      strings.TrimSpace(r.PostForm.Get("valid_from")),
		ExpiresAt:      strings.TrimSpace(r.PostForm.Get("expires_at")),
		MaxAllowed:     strings.TrimSpace(r.PostForm.Get("max_allowed")),
		AffiliateEmail: strings.TrimSpace(r.PostForm.Get("affiliate_email")),
	}
}

func updateAdminDiscount(ctx *config.AppContext, conf *types.Conf, form DiscountForm, expr string) error {
	discount, err := adminDiscountForConf(ctx, conf, form.ID)
	if err != nil {
		return err
	}
	if discount == nil {
		return fmt.Errorf("Discount code not found for this event.")
	}
	if err := codeNameAvailableForUpdate(ctx, form.ID, form.CodeName); err != nil {
		return err
	}
	return getters.UpdateDiscount(ctx, form.ID, getters.DiscountInput{
		CodeName:       strings.ToUpper(form.CodeName),
		DiscountExpr:   expr,
		ConfRef:        conf.Ref,
		AffiliateEmail: form.AffiliateEmail,
	})
}

func deleteAdminDiscount(ctx *config.AppContext, conf *types.Conf, discountID string) error {
	discount, err := adminDiscountForConf(ctx, conf, discountID)
	if err != nil {
		return err
	}
	if discount == nil {
		return fmt.Errorf("Discount code not found for this event.")
	}
	return getters.ArchiveDiscount(ctx, discountID)
}

func adminDiscountForConf(ctx *config.AppContext, conf *types.Conf, discountID string) (*types.DiscountCode, error) {
	if strings.TrimSpace(discountID) == "" {
		return nil, fmt.Errorf("Discount ID is required.")
	}
	discounts, err := getters.FetchDiscountsCached(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range discounts {
		if d == nil || d.Ref != discountID {
			continue
		}
		for _, ref := range d.ConfRef {
			if ref == conf.Ref {
				return d, nil
			}
		}
		return nil, nil
	}
	return nil, nil
}

func codeNameAvailableForUpdate(ctx *config.AppContext, discountID, codeName string) error {
	discounts, err := getters.FetchDiscountsCached(ctx)
	if err != nil {
		return fmt.Errorf("Couldn't check whether that code already exists.")
	}
	target := strings.ToUpper(strings.TrimSpace(codeName))
	for _, d := range discounts {
		if d == nil || d.Ref == discountID {
			continue
		}
		if strings.ToUpper(d.CodeName) == target {
			return fmt.Errorf("That code already exists. Pick another code name.")
		}
	}
	return nil
}

func buildDiscountExpr(form DiscountForm) (string, error) {
	code := strings.TrimSpace(form.CodeName)
	if code == "" {
		return "", fmt.Errorf("Code name is required.")
	}
	if strings.ContainsAny(code, " \t\r\n") {
		return "", fmt.Errorf("Code name cannot contain spaces.")
	}
	if form.AffiliateEmail != "" {
		if _, err := mail.ParseAddress(form.AffiliateEmail); err != nil {
			return "", fmt.Errorf("Affiliate email must be a valid email address, or blank.")
		}
	}
	amount, err := strconv.ParseUint(form.Amount, 10, 32)
	if err != nil || amount == 0 {
		return "", fmt.Errorf("Amount must be a positive whole number.")
	}

	var prefix string
	switch form.DiscountType {
	case "percent":
		if amount > 100 {
			return "", fmt.Errorf("Percent off cannot be more than 100.")
		}
		prefix = "%"
	case "dollars":
		prefix = "$"
	default:
		return "", fmt.Errorf("Choose percent off or dollars off.")
	}

	expr := fmt.Sprintf("%s%d", prefix, amount)
	if form.MaxAllowed != "" {
		maxUses, err := strconv.ParseUint(form.MaxAllowed, 10, 32)
		if err != nil || maxUses == 0 {
			return "", fmt.Errorf("Max allowed must be a positive whole number, or blank for unlimited.")
		}
		expr += fmt.Sprintf(":%d", maxUses)
	}

	from, err := parseDiscountAdminDate(form.ValidFrom)
	if err != nil {
		return "", fmt.Errorf("Valid from must be a valid date.")
	}
	until, err := parseDiscountAdminDate(form.ExpiresAt)
	if err != nil {
		return "", fmt.Errorf("Expires at must be a valid date.")
	}
	if from != "" && until != "" && until < from {
		return "", fmt.Errorf("Expires at must be the same day or after valid from.")
	}
	switch {
	case from != "" && until != "":
		expr += "@" + from + "-" + until
	case from != "":
		expr += "@" + from + "-"
	case until != "":
		expr += "<" + until
	}

	dc := &types.DiscountCode{Discount: expr}
	if err := dc.ParseDiscountExpr(); err != nil {
		return "", fmt.Errorf("Generated discount expression is invalid: %s", err)
	}
	return expr, nil
}

func parseDiscountAdminDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return "", err
	}
	return t.Format("20060102"), nil
}

func discountsForConf(ctx *config.AppContext, conf *types.Conf) []AdminDiscountRow {
	discounts, err := getters.FetchDiscountsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/%s/admin/discounts fetch: %s", conf.Tag, err)
		return nil
	}
	matches := make([]*types.DiscountCode, 0)
	for _, d := range discounts {
		if d == nil {
			continue
		}
		for _, ref := range d.ConfRef {
			if ref == conf.Ref {
				matches = append(matches, d)
				break
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return strings.ToUpper(matches[i].CodeName) < strings.ToUpper(matches[j].CodeName)
	})

	out := make([]AdminDiscountRow, 0, len(matches))
	for _, d := range matches {
		out = append(out, adminDiscountRow(d))
	}
	return out
}

func adminDiscountRow(d *types.DiscountCode) AdminDiscountRow {
	amount := "-"
	switch d.DiscType {
	case '%':
		amount = fmt.Sprintf("%d%% off", d.Amount)
	case '$':
		amount = fmt.Sprintf("$%d off", d.Amount)
	case '=':
		amount = fmt.Sprintf("$%d fixed", d.Amount)
	}

	from := "anytime"
	if d.ValidFrom != nil {
		from = d.ValidFrom.Format("2006-01-02")
	}
	until := "no expiry"
	if d.ValidUntil != nil {
		until = d.ValidUntil.Format("2006-01-02")
	}

	max := "unlimited"
	if d.MaxUses > 0 {
		max = strconv.FormatUint(uint64(d.MaxUses), 10)
	}

	return AdminDiscountRow{
		ID:             d.Ref,
		CodeName:       d.CodeName,
		Expression:     d.Discount,
		AmountLabel:    amount,
		ValidDates:     from + " to " + until,
		UsesLabel:      fmt.Sprintf("%d / %s", d.UsesCount, max),
		AffiliateEmail: d.AffiliateEmail,
		Form:           discountFormFromCode(d),
	}
}

func discountFormFromCode(d *types.DiscountCode) DiscountForm {
	form := DiscountForm{
		ID:             d.Ref,
		CodeName:       d.CodeName,
		Amount:         strconv.FormatUint(uint64(d.Amount), 10),
		AffiliateEmail: d.AffiliateEmail,
	}
	switch d.DiscType {
	case '%':
		form.DiscountType = "percent"
	case '$':
		form.DiscountType = "dollars"
	default:
		form.DiscountType = "percent"
	}
	if d.MaxUses > 0 {
		form.MaxAllowed = strconv.FormatUint(uint64(d.MaxUses), 10)
	}
	if d.ValidFrom != nil {
		form.ValidFrom = d.ValidFrom.Format("2006-01-02")
	}
	if d.ValidUntil != nil {
		form.ExpiresAt = d.ValidUntil.Format("2006-01-02")
	}
	return form
}
