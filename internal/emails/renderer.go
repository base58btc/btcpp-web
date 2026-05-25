package emails

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"text/template"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/mtypes"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type SubConfirmEmail struct {
	Email      string
	ConfirmURL string
	Newsletter string
	URI        string
}

/* Blogpost on how to write renderers https://blog.kowalczyk.info/article/cxn3/advanced-markdown-processing-in-go.html */
func emailRenderHook(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
	graphStyles := `
	font-optical-sizing: auto;
	font-style: normal;
	line-height: 1.75rem;
	font-size: 1rem;
	margin-top: 1rem;
	color: rgb(54, 55, 55);
	`
	if paragraph, ok := node.(*ast.Paragraph); ok && entering {
		paragraph.Attribute = &ast.Attribute{
			Attrs: make(map[string][]byte),
		}
		paragraph.Attribute.Attrs["style"] = []byte(graphStyles)
	}
	if image, ok := node.(*ast.Image); ok && entering {
		image.Attribute = &ast.Attribute{
			Attrs: make(map[string][]byte),
		}
		image.Attribute.Attrs["style"] = []byte(`max-width: 95%;`)
	}
	if list, ok := node.(*ast.List); ok && entering {
		styleAttr := `padding-inline-start: 1.5rem;`
		list.Attribute = &ast.Attribute{
			Attrs: make(map[string][]byte),
		}
		list.Attribute.Attrs["style"] = []byte(styleAttr)
	}
	if listItem, ok := node.(*ast.ListItem); ok {
		var toWrite string
		if entering {
			toWrite = fmt.Sprintf(`<li>
			<p styles="%s">`, graphStyles)
		} else {
			toWrite = `</p></li>`
		}
		listItem.Tight = true
		io.WriteString(w, toWrite)
		return ast.GoToNext, true
	}
	if blockquote, ok := node.(*ast.BlockQuote); ok && entering {
		var attr *ast.Attribute
		if c := blockquote.AsContainer(); c != nil {
			if c.Attribute != nil {
				attr = c.Attribute
			} else {
				attr = &ast.Attribute{
					Attrs: make(map[string][]byte, 0),
				}
				c.Attribute = attr
			}
		}
		if attr == nil {
			return ast.GoToNext, false
		}

		styleValue := `
			border: none;
			margin: 0;
			text-align: center;
			align-items: center;
			margin-bottom: auto;
			padding-bottom: 1rem;
			padding-top: 1rem;
		`
		attr.Attrs["style"] = []byte(styleValue)
	}
	if anchor, ok := node.(*ast.Link); ok && entering {
		var styleAttr string
		dest := string(anchor.Destination)
		if strings.HasPrefix(dest, "button#") {
			trimmed := strings.TrimPrefix(dest, "button#")
			anchor.Destination = []byte(trimmed)

			styleAttr = `style="
				color: #3f3f3f;
				background-color: #fff;
				border: 1.5px solid #f7931a;
				border-radius: 6px;
				cursor: pointer;
				display: inline-block;
				line-height: inherit;
				padding: .75rem 1.5rem;
				font-family: tenon, sans-serif;
				font-size: 1.1rem;
				font-weight: 500;
				text-align: center;
				text-decoration: none;
			"`
		} else {
			styleAttr = `style="text-decoration-line:underline; text-underline-offset:4px; font-weight:400;"`
		}
		anchor.AdditionalAttributes = append(anchor.AdditionalAttributes, styleAttr)
	}
	if head, ok := node.(*ast.Heading); ok && entering {
		styleAttr := ""
		switch head.Level {
		case 1:
			styleAttr = `color: rgb(17 24 39); letter-spacing: -.025em; font-weight: 700; font-size: 2.25rem; line-height: 2.5rem;`
		case 2:
			styleAttr = `color: rgb(17 24 39); letter-spacing: -.025em; font-weight: 700; font-size: 2.25rem; line-height: 2.5rem;`
		case 3:
			styleAttr = `color:rgb(55 65 81);letter-spacing:-.025em;font-weight:700;font-size:1.5rem;line-height:2rem;margin-top:2rem;`
		}

		if styleAttr != "" {
			head.Attribute = &ast.Attribute{
				Attrs: make(map[string][]byte),
			}
			head.Attribute.Attrs["style"] = []byte(styleAttr)
		}
	}

	return ast.GoToNext, false
}

func newEmailRenderer() *html.Renderer {
	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{
		RenderNodeHook: emailRenderHook,
		Flags:          htmlFlags,
	}
	return html.NewRenderer(opts)
}

func mdToHTML(md []byte) []byte {
	/* create markdown parser with extensions */
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(md)

	/* Create HTML renderer with extensions */
	renderer := newEmailRenderer()

	return markdown.Render(doc, renderer)
}

func missiveTemplate(ctx *config.AppContext, letter *mtypes.Letter) *template.Template {

	/* Hash the data for a key. We use the ID + body
	 * so if they change, a new template will get generated */
	keyhash := helpers.MakeJobHash("", letter.UID, letter.Markdown)
	t, ok := ctx.EmailCache[keyhash]
	if !ok {
		tmpl := template.New("")
		if letter.OnlyFor == mtypes.OnlyForTemplated {
			tmpl = tmpl.Funcs(templatedNewsletterFuncs())
		}
		t = template.Must(tmpl.Parse(string(letter.Markdown)))
		ctx.EmailCache[keyhash] = t
	}

	return t
}

func findEmailMarkdown(ctx *config.AppContext, tmplURL string) (*template.Template, error) {
	t, ok := ctx.EmailCache[tmplURL]
	if !ok {
		ctx.Infos.Printf("cache miss for %s", tmplURL)
		req, err := http.NewRequest("GET", tmplURL, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		tmpl, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("error returned from %s: status %d", tmplURL, resp.StatusCode)
		}

		t = template.Must(template.New("").Parse(string(tmpl)))
		ctx.EmailCache[tmplURL] = t
	}

	return t, nil
}

func BuildHTMLEmail(ctx *config.AppContext, markdown []byte) ([]byte, error) {
	defaultImg := "/static/img/newsletter/logo_blk.svg"
	return BuildHTMLEmailUnsub(ctx, defaultImg, markdown, "")
}

func BuildHTMLEmailUnsub(ctx *config.AppContext, imgRef string, markdown []byte, unsubscribe string) ([]byte, error) {

	/* Convert markdown to HTML */
	htmlOut := mdToHTML(markdown)

	/* Embed into our email wrapper template */
	var email bytes.Buffer
	err := ctx.TemplateCache.ExecuteTemplate(&email, "emails/tmp.tmpl", &mtypes.EmailContent{
		Content:     string(htmlOut),
		ImgRef:      imgRef,
		URI:         ctx.Env.GetURI(),
		Unsubscribe: unsubscribe,
	})

	if err != nil {
		return nil, err
	}

	return email.Bytes(), nil
}

type templatedNewsletterConfig struct {
	Template string
	Palette  string
	Issue    string
	Date     string
	Hero     string
	Ticker   []string
}

type templatedNewsletterEmail struct {
	Content     htmltemplate.HTML
	ImgRef      string
	URI         string
	Unsubscribe string
	Config      templatedNewsletterConfig
	Styles      htmltemplate.CSS
}

func BuildTemplatedNewsletterEmail(ctx *config.AppContext, imgRef string, markdown []byte, unsubscribe string) ([]byte, []byte, error) {
	return BuildTemplatedNewsletterEmailAt(ctx, imgRef, markdown, unsubscribe, time.Time{})
}

func BuildTemplatedNewsletterEmailAt(ctx *config.AppContext, imgRef string, markdown []byte, unsubscribe string, displayTime time.Time) ([]byte, []byte, error) {
	cfg, body := parseTemplatedNewsletterFrontmatter(string(markdown))
	if cfg.Template == "" {
		cfg.Template = "roundup"
	}
	if cfg.Palette == "" {
		cfg.Palette = "ember"
	}
	if cfg.Issue == "" {
		cfg.Issue = "NEWSLETTER"
	}
	if !displayTime.IsZero() {
		cfg.Date = formatTemplatedNewsletterDate(displayTime)
	} else if cfg.Date == "" {
		cfg.Date = formatTemplatedNewsletterDate(time.Now())
	}
	if cfg.Hero == "" {
		cfg.Hero = imgRef
	}

	htmlOut := mdToHTML([]byte(body))
	var email bytes.Buffer
	err := ctx.TemplateCache.ExecuteTemplate(&email, "emails/rebrand.tmpl", &templatedNewsletterEmail{
		Content:     htmltemplate.HTML(htmlOut),
		ImgRef:      imgRef,
		URI:         ctx.Env.GetURI(),
		Unsubscribe: unsubscribe,
		Config:      cfg,
		Styles:      rebrandEmailCSS(cfg.Palette),
	})
	if err != nil {
		return nil, nil, err
	}

	return email.Bytes(), []byte(body), nil
}

func formatTemplatedNewsletterDate(t time.Time) string {
	return strings.ToUpper(t.UTC().Format("Jan 02, 2006"))
}

func parseTemplatedNewsletterFrontmatter(input string) (templatedNewsletterConfig, string) {
	var cfg templatedNewsletterConfig
	input = strings.ReplaceAll(input, "\r\n", "\n")
	if !strings.HasPrefix(input, "---\n") {
		return cfg, input
	}
	end := strings.Index(input[4:], "\n---")
	if end == -1 {
		return cfg, input
	}
	raw := input[4 : 4+end]
	body := strings.TrimLeft(input[4+end+len("\n---"):], "\n")

	var currentList string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentList == "ticker" {
			cfg.Ticker = append(cfg.Ticker, trimMetaValue(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		currentList = ""
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := trimMetaValue(parts[1])
		switch key {
		case "template":
			cfg.Template = value
		case "palette":
			cfg.Palette = value
		case "issue":
			cfg.Issue = value
		case "hero":
			cfg.Hero = value
		case "ticker":
			currentList = "ticker"
			if value != "" {
				cfg.Ticker = splitMetaList(value)
			}
		}
	}

	return cfg, body
}

func trimMetaValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}

func splitMetaList(value string) []string {
	value = strings.Trim(value, "[]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = trimMetaValue(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func templatedNewsletterFuncs() template.FuncMap {
	return template.FuncMap{
		"button": func(label, href string) string {
			return rebrandButton(label, href)
		},
		"cta": func(eyebrow, title, subtitle, label, href string) string {
			return rebrandCTA(eyebrow, title, subtitle, label, href)
		},
		"hero": func(src, caption string) string {
			return rebrandHero(src, caption)
		},
		"lead": func(eyebrow, title, deck string) string {
			return rebrandLead(eyebrow, title, deck)
		},
		"newsList": func(items ...string) string {
			return rebrandNewsList(items)
		},
		"pullquote": func(quote, by string) string {
			return rebrandPullquote(quote, by)
		},
		"stats": func(items ...string) string {
			return rebrandStats(items)
		},
	}
}

func rebrandEmailCSS(palette string) htmltemplate.CSS {
	paper, outer := rebrandPalette(palette)
	return htmltemplate.CSS(fmt.Sprintf(`
body { margin: 0; padding: 0; background: %s; }
a { color: inherit; }
.btcpp-shell { background: %s; }
.btcpp-inner { width: 640px; max-width: 100%%; background: %s; color: #1C1C1E; border: 1px solid #1C1C1E; font-family: 'IBM Plex Sans', -apple-system, BlinkMacSystemFont, 'Helvetica Neue', Arial, sans-serif; }
.btcpp-row { padding: 24px 32px; border-bottom: 1px solid #1C1C1E; }
.btcpp-content p { font-size: 15px; line-height: 1.65; margin: 16px 0; color: #1C1C1E; }
.btcpp-content h1 { font-size: 48px; line-height: .95; letter-spacing: -2px; margin: 0 0 12px; }
.btcpp-content h2 { font-size: 30px; line-height: 1.05; margin: 28px 0 12px; }
.btcpp-content h3 { font-size: 20px; line-height: 1.2; margin: 24px 0 8px; }
.btcpp-content ul, .btcpp-content ol { padding-left: 24px; }
.btcpp-content li { font-size: 15px; line-height: 1.6; margin: 8px 0; }
.btcpp-section-label { color: #F57247; font-family: 'IBM Plex Mono', ui-monospace, Menlo, Consolas, monospace; font-size: 11px; letter-spacing: 1.5px; text-transform: uppercase; margin-bottom: 8px; }
`, outer, outer, paper))
}

func rebrandPalette(name string) (paper string, outer string) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "graphite":
		return "#FBFBFC", "#E7E8EB"
	case "signal":
		return "#FDFBF4", "#E9E4D2"
	default:
		return "#FFFFFF", "#F1F0ED"
	}
}

func rebrandButton(label, href string) string {
	return fmt.Sprintf(`<a href="%s" style="display:inline-block;background:#F57247;color:#000;border:1px solid #F57247;padding:14px 24px;font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:12px;font-weight:700;letter-spacing:1.5px;text-decoration:none;text-transform:uppercase;">%s &#8594;</a>`, htmltemplate.HTMLEscapeString(href), htmltemplate.HTMLEscapeString(label))
}

func rebrandCTA(eyebrow, title, subtitle, label, href string) string {
	return fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-top:1px solid #1C1C1E;border-bottom:1px solid #1C1C1E;background:#F57247;"><tr><td style="padding:32px 28px;"><div style="font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:11px;letter-spacing:2px;text-transform:uppercase;margin-bottom:12px;">%s</div><div style="font-size:36px;line-height:1;font-weight:700;letter-spacing:-1.5px;margin-bottom:10px;">%s</div><div style="font-family:Georgia,'Times New Roman',serif;font-size:18px;line-height:1.35;margin-bottom:20px;">%s</div><a href="%s" style="display:inline-block;background:#1C1C1E;color:#fff;border:1px solid #1C1C1E;padding:14px 24px;font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:12px;font-weight:700;letter-spacing:1.5px;text-decoration:none;text-transform:uppercase;">%s &#8594;</a></td></tr></table>`,
		htmltemplate.HTMLEscapeString(eyebrow), htmltemplate.HTMLEscapeString(title), htmltemplate.HTMLEscapeString(subtitle), htmltemplate.HTMLEscapeString(href), htmltemplate.HTMLEscapeString(label))
}

func rebrandHero(src, caption string) string {
	out := fmt.Sprintf(`<img src="%s" width="640" height="270" style="width:100%%;max-width:640px;height:270px;max-height:270px;object-fit:cover;display:block;border-bottom:1px solid #1C1C1E;">`, htmltemplate.HTMLEscapeString(src))
	if strings.TrimSpace(caption) == "" {
		return out
	}
	return out + fmt.Sprintf(`<div style="padding:10px 32px;background:#E8E2D5;font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:10px;letter-spacing:1.5px;color:#6B655C;border-bottom:1px solid #1C1C1E;">&#8627; %s</div>`, htmltemplate.HTMLEscapeString(caption))
}

func rebrandLead(eyebrow, title, deck string) string {
	return fmt.Sprintf(`<div class="btcpp-section-label">%s</div><h1 style="font-size:48px;line-height:.95;letter-spacing:-2px;font-weight:700;margin:0 0 12px;color:#1C1C1E;">%s</h1><div style="font-family:Georgia,'Times New Roman',serif;font-style:italic;font-size:20px;line-height:1.35;color:#6B655C;">%s</div>`,
		htmltemplate.HTMLEscapeString(eyebrow), htmltemplate.HTMLEscapeString(title), htmltemplate.HTMLEscapeString(deck))
}

func rebrandNewsList(items []string) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0">`)
	for i, raw := range items {
		parts := splitPipeFields(raw)
		title, blurb, tag, href := fieldAt(parts, 0), fieldAt(parts, 1), fieldAt(parts, 2), fieldAt(parts, 3)
		b.WriteString(`<tr><td style="padding:18px 0;border-top:1px solid #1C1C1E;">`)
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>`)
		b.WriteString(fmt.Sprintf(`<td style="width:48px;vertical-align:top;font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:13px;color:#F57247;font-weight:600;">%02d</td>`, i+1))
		b.WriteString(`<td style="vertical-align:top;">`)
		if href != "" {
			b.WriteString(fmt.Sprintf(`<a href="%s" style="color:#1C1C1E;text-decoration:none;">`, htmltemplate.HTMLEscapeString(href)))
		}
		b.WriteString(fmt.Sprintf(`<div style="font-size:18px;font-weight:700;letter-spacing:-.3px;line-height:1.2;">%s</div>`, htmltemplate.HTMLEscapeString(title)))
		if href != "" {
			b.WriteString(`</a>`)
		}
		b.WriteString(fmt.Sprintf(`<div style="font-family:Georgia,'Times New Roman',serif;font-style:italic;font-size:15px;color:#6B655C;margin-top:4px;line-height:1.4;">%s</div>`, htmltemplate.HTMLEscapeString(blurb)))
		if href != "" {
			b.WriteString(`<div style="font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:11px;color:#F57247;margin-top:8px;letter-spacing:1px;">READ &#8594;</div>`)
		}
		b.WriteString(`</td>`)
		b.WriteString(fmt.Sprintf(`<td style="width:80px;vertical-align:top;text-align:right;font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:10px;letter-spacing:1px;color:#6B655C;">%s</td>`, htmltemplate.HTMLEscapeString(strings.ToUpper(tag))))
		b.WriteString(`</tr></table></td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}

func rebrandPullquote(quote, by string) string {
	return fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-left:4px solid #F57247;"><tr><td style="padding:8px 0 8px 20px;"><div style="font-family:Georgia,'Times New Roman',serif;font-style:italic;font-size:24px;line-height:1.3;color:#1C1C1E;">&ldquo;%s&rdquo;</div><div style="font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:11px;color:#6B655C;letter-spacing:1px;margin-top:12px;text-transform:uppercase;">%s</div></td></tr></table>`, htmltemplate.HTMLEscapeString(quote), htmltemplate.HTMLEscapeString(by))
}

func rebrandStats(items []string) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-top:1px solid #1C1C1E;border-bottom:1px solid #1C1C1E;"><tr>`)
	for _, raw := range items {
		parts := splitPipeFields(raw)
		b.WriteString(fmt.Sprintf(`<td style="padding:18px 12px;text-align:center;border-right:1px solid #1C1C1E;"><div style="font-size:36px;font-weight:700;line-height:1;letter-spacing:-1px;">%s</div><div style="font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;font-size:10px;letter-spacing:1px;text-transform:uppercase;color:#6B655C;margin-top:6px;">%s</div></td>`, htmltemplate.HTMLEscapeString(fieldAt(parts, 0)), htmltemplate.HTMLEscapeString(fieldAt(parts, 1))))
	}
	b.WriteString(`</tr></table>`)
	return b.String()
}

func splitPipeFields(raw string) []string {
	parts := strings.Split(raw, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func fieldAt(parts []string, idx int) string {
	if idx < 0 || idx >= len(parts) {
		return ""
	}
	return parts[idx]
}
