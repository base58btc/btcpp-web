package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/base58btc/btcpp-web/external/getters"
	"github.com/base58btc/btcpp-web/internal/config"
	"github.com/base58btc/btcpp-web/internal/types"
	mailer "github.com/base58btc/mailer/mail"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

var rezziesSent map[string]*types.Registration

func CheckForNewMails(ctx *config.AppContext) {

	if rezziesSent == nil {
		rezziesSent = make(map[string]*types.Registration)
	}

	var success, fails, resent int
	rezzies, err := getters.FetchBtcppRegistrations(ctx, true)
	if err != nil {
		ctx.Err.Println(err)
		return
	}

	for _, rez := range rezzies {
		/* check local list (has sent already?) gets lost on restart */
		_, has := rezziesSent[rez.RefID]
		if has {
			continue
		}

		err = SendMail(ctx, rez)
		if err == nil {
			rezziesSent[rez.RefID] = rez
			success++
		} else if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			rezziesSent[rez.RefID] = rez
			resent++
		} else {
			ctx.Err.Printf("Unable to send mail: %s", err.Error())
			fails++
		}
	}
	if (success+fails+resent > 0) {
		ctx.Infos.Printf("Of %d, sent %d mails, %d failed, %d retries", success+fails+resent, success, fails, resent)
	}
}

func pdfGrabber(url string, res *[]byte) chromedp.Tasks {
	return chromedp.Tasks{
		emulation.SetUserAgentOverride("WebScraper 1.0"),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().WithPrintBackground(true).WithPreferCSSPageSize(true).WithPaperWidth(3.8).WithPaperHeight(12.0).Do(ctx)
			if err != nil {
				return err
			}
			*res = buf
			return nil
		}),
	}
}

func buildChromePdf(ctx *config.AppContext, fromURL string) ([]byte, error) {
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
	if err := chromedp.Run(taskCtx, pdfGrabber(fromURL, &pdfBuffer)); err != nil {
		return pdfBuffer, err
	}

	return pdfBuffer, nil
}

func MakeTicketPDF(ctx *config.AppContext, rez *types.Registration) ([]byte, error) {
	ticketPage := fmt.Sprintf("http://localhost:%s/ticket/%s?type=%s&conf=%s", ctx.Env.Port, rez.RefID, rez.Type, rez.ConfRef)
	return buildChromePdf(ctx, ticketPage)
}

func SendMail(ctx *config.AppContext, rez *types.Registration) error {
	pdf, err := MakeTicketPDF(ctx, rez)
	if err != nil {
		return err
	}

	tickets := make([]*types.Ticket, 1)
	tickets[0] = &types.Ticket{
		Pdf: pdf,
		ID:  rez.RefID,
	}

	return SendTickets(ctx, tickets, rez.ConfRef, rez.Email, time.Now())
}

/* Send a request to our mailer to send a ticket at time */
func SendTickets(ctx *config.AppContext, tickets []*types.Ticket, confRef, email string, sendAt time.Time) error {
	/* Send the ticket email! */
	conf := findConfByRef(ctx, confRef)
	if conf == nil {
		return fmt.Errorf("No conference found for ref %s", confRef)
	}

	var htmlBody bytes.Buffer
	tmpl := fmt.Sprintf("emails/email-html-%s", conf.Tag)
	err := ctx.TemplateCache.ExecuteTemplate(io.Writer(&htmlBody), tmpl, &EmailTmpl{
		URI: ctx.Env.GetURI(),
		CSS: MiniCss(),
		ConfTag: conf.Tag,
	})
	if err != nil {
		return err
	}

	if len(tickets) == 0 {
		return fmt.Errorf("No tickets present!")
	}

	var textBody bytes.Buffer
	tmpl = fmt.Sprintf("emails/email-text-%s", conf.Tag)
	err = ctx.TemplateCache.ExecuteTemplate(io.Writer(&textBody), tmpl, &EmailTmpl{
		URI: ctx.Env.GetURI(),
		ConfTag: conf.Tag,
	})
	if err != nil {
		return err
	}

	var attaches mailer.AttachSet
	attaches = make([]*mailer.Attachment, len(tickets))
	for i, ticket := range tickets {
		attaches[i] = &mailer.Attachment{
			Content: ticket.Pdf,
			Type:    "application/pdf",
			Name:    fmt.Sprintf("btcpp_%s_ticket_%s.pdf", conf.Tag, ticket.ID[:6]),
		}
	}

	ticketJob := tickets[0].ID
	/* Hack to push thru the test ticket, every time! */
	if !ctx.Env.Prod && ticketJob == "testticket" {
		ticketJob = ticketJob + strconv.Itoa(int(sendAt.UTC().Unix()))
	} else if !ctx.Env.Prod && email != "stripe@example.com" {
		ctx.Infos.Printf("About to send ticket to %s, but desisting, not prod!\n", email)
		return nil
	}

	if email == "stripe@example.com" {
		email = "niftynei@gmail.com"
	}

	ctx.Infos.Printf("Sending ticket to %s\n", email)

	title := fmt.Sprintf("[%s] Your Conference Pass is Here!", conf.Desc)

	/* Build a mail to send */
	mail := &mailer.MailRequest{
		JobKey:      fmt.Sprintf("%s-%s", "btcpp", ticketJob),
		ToAddr:      email,
		FromAddr:    "hello@btcpp.dev",
		FromName:    "bitcoin++ ✨",
		Title:       title,
		HTMLBody:    htmlBody.String(),
		TextBody:    textBody.String(),
		Attachments: attaches,
		SendAt:      float64(sendAt.UTC().Unix()),
	}

	return SendMailRequest(ctx, mail)
}

func makeAuthStamp(secret string, timestamp string, r *http.Request) string {
	h := sha256.New()
	h.Write([]byte(secret))
	h.Write([]byte(timestamp))
	h.Write([]byte(r.URL.Path))
	h.Write([]byte(r.Method))
	return hex.EncodeToString(h.Sum(nil))
}

func SendMailRequest(ctx *config.AppContext, mail *mailer.MailRequest) error {
	/* Send as a PUT request w/ JSON body */
	payload, err := json.Marshal(mail)
	if err != nil {
		return err
	}

	client := &http.Client{}

	url := "http://45.55.129.100:9998/job"
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	timestamp := strconv.Itoa(int(time.Now().UTC().Unix()))
	secret := ctx.Env.MailerSecret
	authStamp := makeAuthStamp(secret, timestamp, req)

	req.Header.Set("Authorization", authStamp)
	req.Header.Set("X-Base58-Timestamp", timestamp)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	var ret mailer.ReturnVal
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &ret); err != nil {
		return err
	}

	if !ret.Success {
		return fmt.Errorf("Unable to schedule mail: %s", ret.Message)
	}
	return nil
}
