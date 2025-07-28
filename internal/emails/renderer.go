package emails

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

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

func missiveTemplate(ctx *config.AppContext, letter *mtypes.Letter) (*template.Template) {

	/* Hash the data for a key. We use the ID + body
	 * so if they change, a new template will get generated */
	keyhash := helpers.MakeJobHash("", letter.UID, letter.Markdown)
	t, ok := ctx.EmailCache[keyhash]
	if !ok {
		t = template.Must(template.New("").Parse(string(letter.Markdown)))
		ctx.EmailCache[keyhash] = t
	}

	return t
}

func findEmailMarkdown(ctx *config.AppContext, tmplURL string) (*template.Template, error) {
	t, ok := ctx.EmailCache[tmplURL]
	if !ok {
		ctx.Infos.Printf("cache miss for %s", tmplURL)
		req, _ := http.NewRequest("GET", tmplURL, nil)
		client := &http.Client{}
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
			return nil, fmt.Errorf("error returned from %s: %s", tmplURL, err.Error())
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
