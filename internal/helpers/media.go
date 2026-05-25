package helpers

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// chromeSem limits concurrent headless Chrome instances
var chromeSem = make(chan struct{}, 4)

const chromeRenderTimeout = 90 * time.Second

func discardChromedpLogf(string, ...interface{}) {}

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

	baseCtx, timeoutCancel := context.WithTimeout(context.Background(), chromeRenderTimeout)
	defer timeoutCancel()

	allocCtx, cancel := chromedp.NewExecAllocator(baseCtx, opts...)
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

	baseCtx, timeoutCancel := context.WithTimeout(context.Background(), chromeRenderTimeout)
	defer timeoutCancel()

	allocCtx, cancel := chromedp.NewExecAllocator(baseCtx, opts...)
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

type MediaRenderer struct {
	ctx           *config.AppContext
	allocCtx      context.Context
	cancelAlloc   context.CancelFunc
	browserCtx    context.Context
	cancelBrowser context.CancelFunc
	mu            sync.Mutex
}

func NewMediaRenderer(ctx *config.AppContext) *MediaRenderer {
	chromeSem <- struct{}{}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("allow-insecure-localhost", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("accept-insecure-certs", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, cancelBrowser := chromedp.NewContext(
		allocCtx,
		chromedp.WithLogf(discardChromedpLogf),
		chromedp.WithErrorf(discardChromedpLogf),
	)

	return &MediaRenderer{
		ctx:           ctx,
		allocCtx:      allocCtx,
		cancelAlloc:   cancelAlloc,
		browserCtx:    browserCtx,
		cancelBrowser: cancelBrowser,
	}
}

func (r *MediaRenderer) Close() {
	if r == nil {
		return
	}
	r.cancelBrowser()
	r.cancelAlloc()
	<-chromeSem
}

func (r *MediaRenderer) BuildChromePng(pdfPage *PDFPage) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("nil media renderer")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	tabCtx, cancelTab := chromedp.NewContext(r.browserCtx)
	defer cancelTab()
	taskCtx, cancelTimeout := context.WithTimeout(tabCtx, chromeRenderTimeout)
	defer cancelTimeout()

	var pngBuffer []byte
	if err := chromedp.Run(taskCtx, pngGrabber(pdfPage, &pngBuffer)); err != nil {
		r.ctx.Err.Printf("error taking screenshot: %s", pdfPage.URL)
		return pngBuffer, err
	}

	return pngBuffer, nil
}

func (r *MediaRenderer) MakeMediaPng(card, path string) ([]byte, error) {
	dimens, ok := types.MediaDimens[card]
	if !ok {
		return nil, fmt.Errorf("can't find card %s", card)
	}

	pg := &PDFPage{
		URL:    r.ctx.Env.GetURI() + signedMediaPath(r.ctx, path),
		Height: dimens.Height,
		Width:  dimens.Width,
	}

	r.ctx.Infos.Printf("PNG URL: %s", pg.URL)
	return r.BuildChromePng(pg)
}

func (r *MediaRenderer) MakeSpeakerPng(confTag, card, speakerID, talkID string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/speaker/%s/%s/%s", confTag, card, talkID, speakerID)
	return r.MakeMediaPng(card, path)
}

func (r *MediaRenderer) MakeTalkPng(confTag, card, talkID string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/talk/%s/%s", confTag, card, talkID)
	return r.MakeMediaPng(card, path)
}

func (r *MediaRenderer) MakeSponsorPng(confTag, card, sponsorRef string) ([]byte, error) {
	path := fmt.Sprintf("/media/imgs/%s/sponsor/%s/%s", confTag, card, sponsorRef)
	return r.MakeMediaPng(card, path)
}

func MakeMediaPng(ctx *config.AppContext, card, path string) ([]byte, error) {
	dimens, ok := types.MediaDimens[card]
	if !ok {
		return nil, fmt.Errorf("can't find card %s", card)
	}

	pg := &PDFPage{
		URL:    ctx.Env.GetURI() + signedMediaPath(ctx, path),
		Height: dimens.Height,
		Width:  dimens.Width,
	}

	ctx.Infos.Printf("PNG URL: %s", pg.URL)
	return BuildChromePng(ctx, pg)
}

func signedMediaPath(ctx *config.AppContext, path string) string {
	q := url.Values{}
	q.Set("mt", CreateScopedHMAC(ctx, "media-render", path))
	return path + "?" + q.Encode()
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
