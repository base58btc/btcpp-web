package helpers

import (
	"context"
	"fmt"

	"github.com/base58btc/btcpp-web/internal/config"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type PDFPage struct {
	URL    string
	Height float64
	Width  float64
}


func pdfGrabber(pdf *PDFPage, res *[]byte) chromedp.Tasks {
	return chromedp.Tasks{
		emulation.SetUserAgentOverride("WebScraper 1.0"),
		chromedp.Navigate(pdf.URL),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().WithPrintBackground(true).WithPreferCSSPageSize(true).WithPaperWidth(pdf.Width).WithPaperHeight(pdf.Height).Do(ctx)
			if err != nil {
				return err
			}
			*res = buf
			return nil
		}),
	}
}

func BuildChromePdf(ctx *config.AppContext, pdfPage *PDFPage) ([]byte, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("allow-insecure-localhost", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("accept-insecure-certs", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	taskCtx, cancel := chromedp.NewContext(
		allocCtx,
		chromedp.WithLogf(ctx.Infos.Printf),
	)
	defer cancel()
	var pdfBuffer []byte
	if err := chromedp.Run(taskCtx, pdfGrabber(pdfPage, &pdfBuffer)); err != nil {
		ctx.Err.Printf("error loading URL: %s", pdfPage.URL)
		return pdfBuffer, err
	}

	return pdfBuffer, nil
}

func MakeSpeakerImage(ctx *config.AppContext, confTag, speakerID, talkID string) ([]byte, error) {

	pdf := &PDFPage{
		URL: fmt.Sprintf("http://localhost:%s/media/imgs/%s/%s/%s", ctx.Env.Port, confTag, talkID, speakerID),
		Height: float64(3.27),
		Width: float64(6.25),
	}

	ctx.Infos.Printf("URL: %s", pdf.URL)

	return BuildChromePdf(ctx, pdf)
}
