package emails

import (
	"bytes"
	"crypto/hmac"
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

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/mtypes"
	"btcpp-web/internal/types"
	mailer "github.com/base58btc/mailer/mail"
	"github.com/gorilla/mux"
)

var rezziesSent map[string]*types.Registration

type EmailTmpl struct {
	URI     string
	CSS     string
	ConfTag string
}

type Mail struct {
	JobKey   string
	Sub      string
	Missive  string
	Email    string
	ReplyTo  string
	Title    string
	SendAt   time.Time
	HTMLBody []byte
	TextBody []byte
	Files    []*EmailFile
}

type EmailFile struct {
	PDF  []byte
	Name string
}

func RegisterEndpoints(r *mux.Router, ctx *config.AppContext) {
	r.HandleFunc("/welcome-email", func(w http.ResponseWriter, r *http.Request) {
		TicketCheck(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/trial-email", func(w http.ResponseWriter, r *http.Request) {
		SendMailTest(w, r, ctx)
	}).Methods("GET")

}

func makeSubKey(email, newsletter string) string {
	/* Hash email+newsletter, take first 8 bytes */
	mac := hmac.New(sha256.New, []byte(email))
	mac.Write([]byte(newsletter))
	hashfix := hex.EncodeToString(mac.Sum(nil)[:8])
	return fmt.Sprintf("%s-%s", newsletter, hashfix)
}

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
	if success+fails+resent > 0 {
		ctx.Infos.Printf("Of %d, sent %d mails, %d failed, %d retries", success+fails+resent, success, fails, resent)
	}
}

func MakeTicketPDF(ctx *config.AppContext, rez *types.Registration) ([]byte, error) {
	pdf := &helpers.PDFPage{
		URL:    fmt.Sprintf("http://localhost:%s/ticket/%s?type=%s&conf=%s", ctx.Env.Port, rez.RefID, rez.Type, rez.ConfRef),
		Height: float64(12.0),
		Width:  float64(3.8),
	}
	return helpers.BuildChromePdf(ctx, pdf)
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

func TicketCheck(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confTag, _ := helpers.GetSessionKey("tag", r)

	tmplTag := fmt.Sprintf("emails/%s.tmpl", confTag)
	err := ctx.TemplateCache.ExecuteTemplate(w, tmplTag, &EmailTmpl{
		URI: ctx.Env.GetURI(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Infos.Printf("/welcome-email ExecuteTemplate failed ! %s", err.Error())
	}
}

/* Send a request to our mailer to send a ticket at time */
func SendTickets(ctx *config.AppContext, tickets []*types.Ticket, confRef, email string, sendAt time.Time) error {
	/* Send the ticket email! */
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		return err
	}
	conf := helpers.FindConfByRef(confs, confRef)
	if conf == nil {
		return fmt.Errorf("No conference found for ref %s", confRef)
	}

	var htmlBody bytes.Buffer
	tmpl := fmt.Sprintf("emails/%s.tmpl", conf.Tag)
	err = ctx.TemplateCache.ExecuteTemplate(io.Writer(&htmlBody), tmpl, &EmailTmpl{
		URI:     ctx.Env.GetURI(),
		CSS:     helpers.MiniCss(),
		ConfTag: conf.Tag,
	})
	if err != nil {
		return err
	}

	if len(tickets) == 0 {
		return fmt.Errorf("No tickets present!")
	}

	var textBody bytes.Buffer
	tmpl = fmt.Sprintf("emails/text-%s.tmpl", conf.Tag)
	err = ctx.TemplateCache.ExecuteTemplate(io.Writer(&textBody), tmpl, &EmailTmpl{
		URI:     ctx.Env.GetURI(),
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
		JobKey:      "btcpp-" + ticketJob,
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

func ComposeAndSendMail(ctx *config.AppContext, mail *Mail) error {
	var attaches mailer.AttachSet

	attaches = make([]*mailer.Attachment, len(mail.Files))
	for i, file := range mail.Files {
		attaches[i] = &mailer.Attachment{
			Content: file.PDF,
			Type:    "application/pdf",
			Name:    file.Name,
		}
	}

	/* Build a mail to send */
	mailReq := &mailer.MailRequest{
		JobKey:       "btcpp:" + mail.JobKey,
		Subscription: mail.Sub,
		Missive:      mail.Missive,
		ToAddr:       mail.Email,
		FromAddr:     "hello@btcpp.dev",
		FromName:     "bitcoin++ ✨",
		ReplyTo:      mail.ReplyTo,
		Title:        mail.Title,
		HTMLBody:     string(mail.HTMLBody),
		TextBody:     string(mail.TextBody),
		Attachments:  attaches,
		SendAt:       float64(mail.SendAt.UTC().Unix()),
	}

	return SendMailRequest(ctx, mailReq)
}

func makeAuthStamp(secret string, timestamp string, r *http.Request) string {
	h := sha256.New()
	h.Write([]byte(secret))
	h.Write([]byte(timestamp))
	h.Write([]byte(r.URL.Path))
	h.Write([]byte(r.Method))
	return hex.EncodeToString(h.Sum(nil))
}

func addAuthStamp(ctx *config.AppContext, req *http.Request) {
	timestamp := strconv.Itoa(int(time.Now().UTC().Unix()))
	secret := ctx.Env.MailerSecret
	authStamp := makeAuthStamp(secret, timestamp, req)

	req.Header.Set("Authorization", authStamp)
	req.Header.Set("X-Base58-Timestamp", timestamp)
}

func sendMailerReq(ctx *config.AppContext, endpoint string, method string, payload []byte) error {
	client := &http.Client{}

	url := ctx.Env.MailEndpoint + endpoint
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	addAuthStamp(ctx, req)
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
		return fmt.Errorf("Mailer request %s failed (%d): %s", endpoint, ret.Code, ret.Message)
	}

	return nil
}

func SendSubDeleteRequest(ctx *config.AppContext, email, sub string) error {
	/* Send as a DELETE request w/ JSON body */
	subkey := makeSubKey(email, sub)
	subdelete := &mailer.SubDelete{
		SubKey: subkey,
	}
	payload, err := json.Marshal(subdelete)
	if err != nil {
		return err
	}

	err = sendMailerReq(ctx, "/sub", http.MethodDelete, payload)
	if err != nil {
		return fmt.Errorf("Sub delete request failed. %s, %s : %s", sub, email, err)
	}
	ctx.Infos.Printf("Rm'd subscription %s", subkey)
	return nil
}

func SendCancelMissiveRequest(ctx *config.AppContext, missive *mtypes.Letter) error {
	/* Send as a DELETE request w/ JSON body */
	del := &mailer.MissiveDelete{
		Missive: missive.Missive(),
	}
	payload, err := json.Marshal(del)
	if err != nil {
		return err
	}

	err = sendMailerReq(ctx, "/missive", http.MethodDelete, payload)
	if err != nil {
		return fmt.Errorf("Unable to delete missive %s: %s", del.Missive, err)
	}

	ctx.Infos.Printf("Rm'd missive %s", missive)
	return nil
}

func SendMailRequest(ctx *config.AppContext, mail *mailer.MailRequest) error {
	/* Send as a PUT request w/ JSON body */
	payload, err := json.Marshal(mail)
	if err != nil {
		return err
	}

	err = sendMailerReq(ctx, "/job", http.MethodPut, payload)
	if err != nil {
		return fmt.Errorf("Unable to schedule mail: %s", err)
	}

	ctx.Infos.Printf("Sent mail to %s at domain %s", mail.ToAddr, mail.Domain)
	return nil
}

func SendNewsletterMissive(ctx *config.AppContext, sub *mtypes.Subscriber, letter *mtypes.Letter, sendAt time.Time, preview bool) ([]byte, error) {

	jobhash := helpers.MakeJobHash(sub.Email, letter.UID, letter.Title)
	jobkey := fmt.Sprintf("%s-%s", letter.Missive(), jobhash)

	timestamp := uint64(time.Now().UTC().UnixNano())
	_, newsToken := helpers.GetSubscribeToken(ctx.Env.HMACKey[:], sub.Email, "newsletter", timestamp)

	var buf bytes.Buffer
	err := missiveTemplate(ctx, letter).Execute(&buf, &mtypes.EmailContent{
		ImgRef: letter.ImgRef(),
		URI:    ctx.Env.GetURI(),
		/* Always include the newsletter subscribe token?? */
		SubNewsURL: buildConfirmURL(ctx, newsToken),
	})
	if err != nil {
		return nil, err
	}

	/* Subscription key; ties this missive to all notes meant
	 * for this email/user on this Newsletter */
	subList := letter.SubList(sub)
	if len(subList) == 0 {
		if preview {
			subList = []string{"newsletter"}
		} else {
			return nil, fmt.Errorf("subscriber not sub'ed to this missive?? %s ! %s", letter.Title, sub.Email)
		}
	}

	var subkey, subToken string
	if unsub := letter.Unsub(sub); unsub != "" {
		subkey = makeSubKey(sub.Email, unsub)
		_, subToken = helpers.GetSubscribeToken(ctx.Env.HMACKey[:], sub.Email, unsub, timestamp)
	} else {
		subkey = makeSubKey(sub.Email, subList[0])
	}

	htmlBody, err := BuildHTMLEmailUnsub(ctx, letter.ImgRef(), buf.Bytes(), subToken)
	if err != nil {
		return nil, err
	}
	mail := &Mail{
		JobKey:   jobkey,
		Sub:      subkey,
		Missive:  letter.Missive(),
		Email:    sub.Email,
		Title:    letter.Title,
		SendAt:   sendAt,
		TextBody: buf.Bytes(),
		HTMLBody: htmlBody,
	}

	ctx.Infos.Printf("Sending (%s)%s to %s at %s", subkey, letter.Title, sub.Email, sendAt)

	return htmlBody, ComposeAndSendMail(ctx, mail)
}

func buildConfirmURL(ctx *config.AppContext, token string) string {
	return fmt.Sprintf("%s/confirm/%s", ctx.Env.GetURI(), token)
}

func SendNewsletterSubEmail(ctx *config.AppContext, email, token, newsletter string) ([]byte, error) {

	var title, template string
	title = "Mailing List Subscription"
	template = "emails/confirm-sub.tmpl"
	jobkey := "subscribe-" + token
	mail := &Mail{
		JobKey: jobkey,
		Sub:    makeSubKey(email, newsletter),
		Email:  email,
		Title:  fmt.Sprintf("[Action Required] Confirm bitcoin++ %s", title),
		SendAt: time.Now(),
	}

	ctx.Infos.Printf("mail subkey is %s", mail.Sub)

	/* Swap in the tokens */
	var buf bytes.Buffer
	err := ctx.TemplateCache.ExecuteTemplate(&buf, template, &SubConfirmEmail{
		Email:      email,
		ConfirmURL: buildConfirmURL(ctx, token),
		Newsletter: newsletter,
		URI:        ctx.Env.GetURI(),
	})

	if err != nil {
		return nil, err
	}

	mail.TextBody = buf.Bytes()

	mail.HTMLBody, err = BuildHTMLEmail(ctx, buf.Bytes())
	if err != nil {
		return nil, err
	}

	return mail.HTMLBody, ComposeAndSendMail(ctx, mail)
}

func SendMailTest(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	reg := &types.Registration{
		RefID:      "testticket",
		Type:       "volunteer",
		Email:      "niftynei@gmail.com",
		ItemBought: "bitcoin++",
	}

	sendMail(w, r, ctx, reg)
}

func sendMail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, reg *types.Registration) {
	pdf, err := MakeTicketPDF(ctx, reg)

	if err != nil {
		http.Error(w, "Unable to make ticket, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/send test mail failed ! %s", err.Error())
		return
	}

	tickets := make([]*types.Ticket, 1)
	tickets[0] = &types.Ticket{
		Pdf: pdf,
		ID:  reg.RefID,
	}

	err = SendTickets(ctx, tickets, reg.ConfRef, reg.Email, time.Now())

	/* Return the error */
	if err != nil {
		http.Error(w, "Unable to send ticket, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/send test mail failed to send! %s", err.Error())
		return
	}
}
