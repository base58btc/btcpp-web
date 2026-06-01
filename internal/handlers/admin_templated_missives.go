package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	netmail "net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/missives"
	"btcpp-web/internal/mtypes"
)

type TemplatedMissivesPage struct {
	Letters      []*mtypes.Letter
	Current      *mtypes.Letter
	Form         TemplatedMissiveForm
	IsNew        bool
	FlashMessage string
	ErrorMessage string
	SpacesReady  bool
	Year         uint
}

type TemplatedMissiveForm struct {
	UID             uint64
	PageID          string
	Title           string
	SendAt          string
	Expiry          string
	Newsletters     string
	Template        string
	Palette         string
	Issue           string
	Hero            string
	Ticker          string
	LeadEyebrow     string
	LeadTitle       string
	LeadDeck        string
	NewsItems       string
	Stats           string
	Pullquote       string
	PullquoteBy     string
	CTAEyebrow      string
	CTATitle        string
	CTASubtitle     string
	CTALabel        string
	CTAURL          string
	ContentMarkdown string
	TestEmail       string
}

func TemplatedMissivesAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	letters, err := getters.ListTemplatedLetters(ctx)
	if err != nil {
		http.Error(w, "Unable to load templated missives", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/missives list failed: %s", err)
		return
	}
	sort.SliceStable(letters, func(i, j int) bool {
		if letters[i].SentAt == nil && letters[j].SentAt != nil {
			return true
		}
		if letters[i].SentAt != nil && letters[j].SentAt == nil {
			return false
		}
		return letters[i].UID > letters[j].UID
	})

	page := &TemplatedMissivesPage{
		Letters:      letters,
		IsNew:        true,
		Form:         defaultTemplatedMissiveForm(),
		FlashMessage: r.URL.Query().Get("flash"),
		ErrorMessage: r.URL.Query().Get("error"),
		SpacesReady:  spaces.IsConfigured(),
		Year:         helpers.CurrentYear(),
	}

	if uidStr := strings.TrimSpace(r.URL.Query().Get("uid")); uidStr != "" {
		uid, err := strconv.ParseUint(uidStr, 10, 64)
		if err != nil {
			http.Error(w, "Bad missive UID", http.StatusBadRequest)
			return
		}
		letter, err := getters.GetLetter(ctx, uid)
		if err != nil {
			http.Error(w, "Missive not found", http.StatusNotFound)
			return
		}
		page.Current = letter
		page.IsNew = false
		page.Form = formFromTemplatedLetter(letter)
	}

	if uploaded := strings.TrimSpace(r.URL.Query().Get("uploaded")); uploaded != "" {
		page.Form.Hero = uploaded
		page.FlashMessage = "Image uploaded to Spaces"
	}

	renderTemplatedMissivesAdmin(w, r, ctx, page)
}

func TemplatedMissivesSave(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadFileBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		redirectTemplatedMissivesErr(w, r, "Bad form: "+err.Error())
		return
	}

	form := templatedMissiveFormFromRequest(r)
	if strings.TrimSpace(form.Title) == "" {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Title is required")
		return
	}

	expiry, err := parseOptionalDate(form.Expiry)
	if err != nil {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Expiry must be YYYY-MM-DD")
		return
	}

	input := getters.MissiveInput{
		Title:       strings.TrimSpace(form.Title),
		Markdown:    buildTemplatedMissiveMarkdown(form),
		SendAt:      strings.TrimSpace(form.SendAt),
		Newsletters: splitCommaList(form.Newsletters),
		OnlyFor:     mtypes.OnlyForTemplated,
		Expiry:      expiry,
	}
	if len(input.Newsletters) == 0 {
		input.Newsletters = []string{"newsletter"}
	}

	if form.UID == 0 {
		letter, err := getters.CreateTemplatedMissive(ctx, input)
		if err != nil {
			renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Create failed: "+err.Error())
			return
		}
		http.Redirect(w, r, "/admin/missives?uid="+strconv.FormatUint(letter.UID, 10)+"&flash="+url.QueryEscape("Templated missive created"), http.StatusSeeOther)
		return
	}

	letter, err := getters.GetLetter(ctx, form.UID)
	if err != nil {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Missive not found: "+err.Error())
		return
	}
	if letter.OnlyFor != mtypes.OnlyForTemplated {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Refusing to edit a non-templated missive")
		return
	}
	if err := getters.UpdateTemplatedMissive(ctx, letter.PageID, input); err != nil {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Update failed: "+err.Error())
		return
	}
	http.Redirect(w, r, "/admin/missives?uid="+strconv.FormatUint(form.UID, 10)+"&flash="+url.QueryEscape("Templated missive updated"), http.StatusSeeOther)
}

func TemplatedMissivesUploadImage(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	if !spaces.IsConfigured() {
		http.Error(w, "spaces not configured", http.StatusInternalServerError)
		return
	}
	limitRequestBody(w, r, maxMultipartBodyBytes)
	raw, contentType, ext, err := readMultipartFile(r, "file")
	if err != nil {
		http.Error(w, "missing or unreadable image", http.StatusBadRequest)
		return
	}
	shortID := imgproc.ShortID(raw)
	key := "newsletter/" + shortID + ext
	if !spaces.Exists(key) {
		if _, err := spaces.Upload(key, raw, contentType, ""); err != nil {
			ctx.Err.Printf("/admin/missives/upload-image: %s", err)
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": spaces.PublicURL(key)})
}

func TemplatedMissivesTestSend(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadFileBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		redirectTemplatedMissivesErr(w, r, "Bad form: "+err.Error())
		return
	}

	form := templatedMissiveFormFromRequest(r)
	form.TestEmail = strings.TrimSpace(r.FormValue("TestEmail"))
	if strings.TrimSpace(form.Title) == "" {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Title is required before sending a test")
		return
	}
	addr, err := netmail.ParseAddress(form.TestEmail)
	if err != nil || addr.Address == "" {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Enter a valid test recipient email")
		return
	}

	letter := templatedMissiveTestLetter(form)
	sub := subscriberForTemplatedMissiveTest(addr.Address, letter)
	if _, err := emails.SendNewsletterMissive(ctx, sub, letter, time.Now(), true); err != nil {
		renderTemplatedMissivesAdminWithForm(w, r, ctx, form, "Test send failed: "+err.Error())
		return
	}
	renderTemplatedMissivesAdminWithMessages(w, r, ctx, form, "Test missive sent to "+addr.Address, "")
}

func TemplatedMissivesSchedule(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadFileBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		redirectTemplatedMissivesErr(w, r, "Bad form: "+err.Error())
		return
	}
	uidValue := strings.TrimSpace(r.FormValue("UID"))
	if uidValue == "" {
		uidValue = strings.TrimSpace(r.URL.Query().Get("uid"))
	}
	uid, err := strconv.ParseUint(uidValue, 10, 64)
	if err != nil || uid == 0 {
		redirectTemplatedMissivesErr(w, r, "Save the missive before scheduling it")
		return
	}
	letter, err := missives.ScheduleMissiveByUID(ctx, uid)
	if err != nil {
		http.Redirect(w, r, "/admin/missives?uid="+strconv.FormatUint(uid, 10)+"&error="+url.QueryEscape("Schedule failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/missives?uid="+strconv.FormatUint(uid, 10)+"&flash="+url.QueryEscape("Scheduled missive "+letter.Missive()), http.StatusSeeOther)
}

func renderTemplatedMissivesAdminWithForm(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, form TemplatedMissiveForm, msg string) {
	renderTemplatedMissivesAdminWithMessages(w, r, ctx, form, "", msg)
}

func renderTemplatedMissivesAdminWithMessages(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, form TemplatedMissiveForm, flash, errMsg string) {
	letters, _ := getters.ListTemplatedLetters(ctx)
	renderTemplatedMissivesAdmin(w, r, ctx, &TemplatedMissivesPage{
		Letters:      letters,
		Form:         form,
		IsNew:        form.UID == 0,
		FlashMessage: flash,
		ErrorMessage: errMsg,
		SpacesReady:  spaces.IsConfigured(),
		Year:         helpers.CurrentYear(),
	})
}

func renderTemplatedMissivesAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, page *TemplatedMissivesPage) {
	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/templated_missives.tmpl", page); err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/missives template failed: %s", err)
	}
}

func defaultTemplatedMissiveForm() TemplatedMissiveForm {
	return TemplatedMissiveForm{
		SendAt:      "now",
		Newsletters: "newsletter",
		Template:    "roundup",
		Palette:     "ember",
		LeadEyebrow: "§ FEATURE",
		CTALabel:    "READ MORE",
	}
}

func formFromTemplatedLetter(letter *mtypes.Letter) TemplatedMissiveForm {
	form := defaultTemplatedMissiveForm()
	form.UID = letter.UID
	form.PageID = letter.PageID
	form.Title = letter.Title
	form.SendAt = letter.SendAt
	form.Newsletters = strings.Join(letter.Newsletters, ", ")
	form.ContentMarkdown = letter.Markdown
	if letter.Expiry != nil {
		form.Expiry = letter.Expiry.Format("2006-01-02")
	}

	cfg, body := parseTemplatedMissiveFrontmatter(letter.Markdown)
	form.ContentMarkdown = body
	if cfg["template"] != "" {
		form.Template = cfg["template"]
	}
	if cfg["palette"] != "" {
		form.Palette = cfg["palette"]
	}
	if cfg["issue"] != "" {
		form.Issue = cfg["issue"]
	}
	if cfg["hero"] != "" {
		form.Hero = cfg["hero"]
	}
	if cfg["ticker"] != "" {
		form.Ticker = cfg["ticker"]
	}
	form.ContentMarkdown = hydrateTemplatedShortcodes(&form, form.ContentMarkdown)
	return form
}

func hydrateTemplatedShortcodes(form *TemplatedMissiveForm, body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	var remaining []string
	for _, line := range strings.Split(body, "\n") {
		name, args, ok := parseTemplatedShortcodeLine(line)
		if !ok {
			remaining = append(remaining, line)
			continue
		}
		switch name {
		case "lead":
			if len(args) >= 3 {
				form.LeadEyebrow = args[0]
				form.LeadTitle = args[1]
				form.LeadDeck = args[2]
				continue
			}
		case "newsList":
			if len(args) > 0 {
				form.NewsItems = strings.Join(args, "\n")
				continue
			}
		case "stats":
			if len(args) > 0 {
				form.Stats = strings.Join(args, "\n")
				continue
			}
		case "pullquote":
			if len(args) >= 1 {
				form.Pullquote = args[0]
				if len(args) >= 2 {
					form.PullquoteBy = args[1]
				}
				continue
			}
		case "cta":
			if len(args) >= 5 {
				form.CTAEyebrow = args[0]
				form.CTATitle = args[1]
				form.CTASubtitle = args[2]
				form.CTALabel = args[3]
				form.CTAURL = args[4]
				continue
			}
		}
		remaining = append(remaining, line)
	}
	return strings.TrimSpace(strings.Join(remaining, "\n"))
}

func parseTemplatedShortcodeLine(line string) (string, []string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return "", nil, false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "{{"), "}}"))
	if inner == "" {
		return "", nil, false
	}
	nameEnd := strings.IndexAny(inner, " \t")
	if nameEnd == -1 {
		return inner, nil, true
	}
	name := inner[:nameEnd]
	args, ok := parseTemplatedQuotedArgs(strings.TrimSpace(inner[nameEnd:]))
	if !ok {
		return "", nil, false
	}
	return name, args, true
}

func parseTemplatedQuotedArgs(input string) ([]string, bool) {
	var args []string
	for {
		input = strings.TrimSpace(input)
		if input == "" {
			return args, true
		}
		if !strings.HasPrefix(input, `"`) {
			return nil, false
		}
		end := -1
		for i := 1; i < len(input); i++ {
			if input[i] == '\\' {
				i++
				continue
			}
			if input[i] == '"' {
				end = i
				break
			}
		}
		if end == -1 {
			return nil, false
		}
		value, err := strconv.Unquote(input[:end+1])
		if err != nil {
			return nil, false
		}
		args = append(args, value)
		input = input[end+1:]
	}
}

func templatedMissiveFormFromRequest(r *http.Request) TemplatedMissiveForm {
	uid, _ := strconv.ParseUint(strings.TrimSpace(r.FormValue("UID")), 10, 64)
	return TemplatedMissiveForm{
		UID:             uid,
		Title:           strings.TrimSpace(r.FormValue("Title")),
		SendAt:          strings.TrimSpace(r.FormValue("SendAt")),
		Expiry:          strings.TrimSpace(r.FormValue("Expiry")),
		Newsletters:     strings.TrimSpace(r.FormValue("Newsletters")),
		Template:        strings.TrimSpace(r.FormValue("Template")),
		Palette:         strings.TrimSpace(r.FormValue("Palette")),
		Issue:           strings.TrimSpace(r.FormValue("Issue")),
		Hero:            strings.TrimSpace(r.FormValue("Hero")),
		Ticker:          strings.TrimSpace(r.FormValue("Ticker")),
		LeadEyebrow:     strings.TrimSpace(r.FormValue("LeadEyebrow")),
		LeadTitle:       strings.TrimSpace(r.FormValue("LeadTitle")),
		LeadDeck:        strings.TrimSpace(r.FormValue("LeadDeck")),
		NewsItems:       strings.TrimSpace(r.FormValue("NewsItems")),
		Stats:           strings.TrimSpace(r.FormValue("Stats")),
		Pullquote:       strings.TrimSpace(r.FormValue("Pullquote")),
		PullquoteBy:     strings.TrimSpace(r.FormValue("PullquoteBy")),
		CTAEyebrow:      strings.TrimSpace(r.FormValue("CTAEyebrow")),
		CTATitle:        strings.TrimSpace(r.FormValue("CTATitle")),
		CTASubtitle:     strings.TrimSpace(r.FormValue("CTASubtitle")),
		CTALabel:        strings.TrimSpace(r.FormValue("CTALabel")),
		CTAURL:          strings.TrimSpace(r.FormValue("CTAURL")),
		ContentMarkdown: strings.TrimSpace(r.FormValue("ContentMarkdown")),
		TestEmail:       strings.TrimSpace(r.FormValue("TestEmail")),
	}
}

func templatedMissiveTestLetter(form TemplatedMissiveForm) *mtypes.Letter {
	uid := form.UID
	if uid == 0 {
		uid = uint64(time.Now().UTC().UnixNano())
	}
	newsletters := splitCommaList(form.Newsletters)
	if len(newsletters) == 0 {
		newsletters = []string{"newsletter"}
	}
	testForm := form
	testForm.SendAt = "now"
	return &mtypes.Letter{
		UID:         uid,
		Title:       "[TEST] " + strings.TrimSpace(form.Title),
		Newsletters: newsletters,
		OnlyFor:     mtypes.OnlyForTemplated,
		Markdown:    buildTemplatedMissiveMarkdown(testForm),
		SendAt:      "now",
	}
}

func subscriberForTemplatedMissiveTest(email string, letter *mtypes.Letter) *mtypes.Subscriber {
	names := letter.InNewsletters()
	if len(names) == 0 {
		names = []string{"newsletter"}
	}
	subs := make([]*mtypes.Subscription, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		subs = append(subs, &mtypes.Subscription{Name: name})
	}
	return &mtypes.Subscriber{Email: email, Subs: subs}
}

func buildTemplatedMissiveMarkdown(form TemplatedMissiveForm) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeFrontmatter(&b, "template", firstNonEmpty(form.Template, "roundup"))
	writeFrontmatter(&b, "palette", firstNonEmpty(form.Palette, "ember"))
	writeFrontmatter(&b, "issue", form.Issue)
	writeFrontmatter(&b, "hero", form.Hero)
	if form.Ticker != "" {
		b.WriteString("ticker:\n")
		for _, item := range splitLines(form.Ticker) {
			b.WriteString("  - ")
			b.WriteString(item)
			b.WriteByte('\n')
		}
	}
	b.WriteString("---\n\n")

	if form.LeadTitle != "" || form.LeadDeck != "" {
		b.WriteString(fmt.Sprintf("{{ lead %q %q %q }}\n\n", firstNonEmpty(form.LeadEyebrow, "§ FEATURE"), form.LeadTitle, form.LeadDeck))
	}
	if form.NewsItems != "" {
		b.WriteString("{{ newsList")
		for _, item := range splitLines(form.NewsItems) {
			b.WriteString(fmt.Sprintf(" %q", item))
		}
		b.WriteString(" }}\n\n")
	}
	if form.Stats != "" {
		b.WriteString("{{ stats")
		for _, item := range splitLines(form.Stats) {
			b.WriteString(fmt.Sprintf(" %q", item))
		}
		b.WriteString(" }}\n\n")
	}
	if form.Pullquote != "" {
		b.WriteString(fmt.Sprintf("{{ pullquote %q %q }}\n\n", form.Pullquote, form.PullquoteBy))
	}
	if form.ContentMarkdown != "" {
		b.WriteString(form.ContentMarkdown)
		b.WriteString("\n\n")
	}
	if form.CTATitle != "" || form.CTAURL != "" {
		b.WriteString(fmt.Sprintf("{{ cta %q %q %q %q %q }}\n", form.CTAEyebrow, form.CTATitle, form.CTASubtitle, firstNonEmpty(form.CTALabel, "READ MORE"), form.CTAURL))
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func writeFrontmatter(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(strconv.Quote(value))
	b.WriteByte('\n')
}

func parseTemplatedMissiveFrontmatter(markdown string) (map[string]string, string) {
	out := map[string]string{}
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	if !strings.HasPrefix(markdown, "---\n") {
		return out, markdown
	}
	end := strings.Index(markdown[4:], "\n---")
	if end == -1 {
		return out, markdown
	}
	raw := markdown[4 : 4+end]
	body := strings.TrimLeft(markdown[4+end+len("\n---"):], "\n")
	var listKey string
	var listItems []string
	flushList := func() {
		if listKey != "" {
			out[listKey] = strings.Join(listItems, "\n")
		}
		listKey = ""
		listItems = nil
	}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && listKey != "" {
			listItems = append(listItems, strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), `"`))
			continue
		}
		flushList()
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		if value == "" && key == "ticker" {
			listKey = key
			continue
		}
		out[key] = value
	}
	flushList()
	return out, body
}

func parseOptionalDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitLines(value string) []string {
	lines := strings.Split(value, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func redirectTemplatedMissivesErr(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/admin/missives?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
