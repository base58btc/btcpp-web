package helpers

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// chromeSem limits concurrent headless Chrome instances
var chromeSem = make(chan struct{}, 4)

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
	chromeSem <- struct{}{}
	defer func() { <-chromeSem }()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("allow-insecure-localhost", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("accept-insecure-certs", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	taskCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	var pdfBuffer []byte
	if err := chromedp.Run(taskCtx, pdfGrabber(pdfPage, &pdfBuffer)); err != nil {
		ctx.Err.Printf("error loading URL: %s", pdfPage.URL)
		return pdfBuffer, err
	}

	return pdfBuffer, nil
}

func pngGrabber(pg *PDFPage, res *[]byte) chromedp.Tasks {
	// Convert inches to pixels at 96 DPI
	widthPx := int64(pg.Width * 96)
	heightPx := int64(pg.Height * 96)

	return chromedp.Tasks{
		emulation.SetUserAgentOverride("WebScraper 1.0"),
		emulation.SetDeviceMetricsOverride(widthPx, heightPx, 1, false),
		chromedp.Navigate(pg.URL),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.FullScreenshot(res, 100),
	}
}

func BuildChromePng(ctx *config.AppContext, pdfPage *PDFPage) ([]byte, error) {
	chromeSem <- struct{}{}
	defer func() { <-chromeSem }()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("allow-insecure-localhost", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("accept-insecure-certs", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	taskCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	var pngBuffer []byte
	if err := chromedp.Run(taskCtx, pngGrabber(pdfPage, &pngBuffer)); err != nil {
		ctx.Err.Printf("error taking screenshot: %s", pdfPage.URL)
		return pngBuffer, err
	}

	return pngBuffer, nil
}

func MakeMediaPng(ctx *config.AppContext, card, path string) ([]byte, error) {
	dimens, ok := types.MediaDimens[card]
	if !ok {
		return nil, fmt.Errorf("can't find card %s", card)
	}

	pg := &PDFPage{
		URL:    ctx.Env.GetURI() + path,
		Height: dimens.Height,
		Width:  dimens.Width,
	}

	ctx.Infos.Printf("PNG URL: %s", pg.URL)
	return BuildChromePng(ctx, pg)
}

func MakeSpeakerPng(ctx *config.AppContext, confTag, card, speakerID, talkID string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/speaker/%s/%s/%s", confTag, card, talkID, speakerID)
	return MakeMediaPng(ctx, card, path)
}

func MakeTalkPng(ctx *config.AppContext, confTag, card, talkID string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/talk/%s/%s", confTag, card, talkID)
	return MakeMediaPng(ctx, card, path)
}

func MakeSponsorPng(ctx *config.AppContext, confTag, card, sponsorRef string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/sponsor/%s/%s", confTag, card, sponsorRef)
	return MakeMediaPng(ctx, card, path)
}

func MakeAgendaImg(ctx *config.AppContext, confTag, dayref, venue string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/agenda/%s/%s", confTag, dayref, venue)
	return MakeMediaPng(ctx, "agenda", path)
}
