package emails

import (
        "bytes"
        "fmt"
	"html/template"
        "time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/mtypes"
	"btcpp-web/internal/types"
)

type (
        VolLogin struct {
                Email        string
                VolShiftLink string
        }

        VolSignup struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                Email        string
                VolShiftLink string
        }

        VolWaitlist struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                Email        string
                VolShiftLink string
        }

        VolShifts struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                VolInfo      *types.VolInfo
                Email        string
                VolShiftLink string
        }

        VolCustom struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                VolInfo      *types.VolInfo
                Email        string
                VolShiftLink string
        }

        VolCancel struct {
                Volunteer    *types.Volunteer
                Conf         *types.Conf
                VolShiftLink string
        }

        VolApp struct {
                Name         string
                Conf         *types.Conf
                VolInfo      *types.VolInfo
                Email        string
                VolShiftLink string
        }

        SpeakerCustom struct {
                Speaker *types.Speaker
                Conf    *types.Conf
                Talks   []*types.Talk
                Email   string
        }
)


func makeJobKeyRep(email string, letter *mtypes.Letter) string {
	jobhash := helpers.MakeJobHash(email, letter.UID, letter.Title)
	return fmt.Sprintf("%s-%s-%d", letter.Missive(), jobhash, time.Now().Unix())
}


func OnlyForVolLogin(ctx *config.AppContext, email string) ([]byte, error) {
        onlyFor := "vollogin"
        tmplData := &VolLogin{
                Email: email,
                VolShiftLink: helpers.EmailLink(ctx, email, "/vols/shift"),
	}

        return execOnlyFor(ctx, email, onlyFor, tmplData)
}

func OnlyForVolWaitlist(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
        onlyFor := "volwaitlist"
        tmplData := &VolWaitlist{
                Email: vol.Email,
                Conf: conf,
                Volunteer: vol,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolSignup(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
        onlyFor := "volsignup"
        tmplData := &VolSignup{
                Email: vol.Email,
                Conf: conf,
                Volunteer: vol,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolApp(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf, volinfo *types.VolInfo) ([]byte, error) {
        onlyFor := "volapp"
        tmplData := &VolApp{
                Name:         vol.Name,
                Conf:         conf,
                VolInfo:      volinfo,
                Email:        vol.Email,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolCancel(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) ([]byte, error) {
        onlyFor := "volcancel"
        tmplData := &VolCancel{
                Volunteer:  vol,
                Conf:  conf,
                VolShiftLink:   helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func OnlyForVolShift(ctx *config.AppContext, volinfo *types.VolInfo, vol *types.Volunteer) ([]byte, error) {
        onlyFor := "volshifts"
        tmplData := &VolShifts{
                Volunteer:      vol,
                Conf:           vol.ScheduleFor[0],
                VolInfo:        volinfo,
                Email:          vol.Email,
                VolShiftLink:   helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
	}

        return execOnlyFor(ctx, vol.Email, onlyFor, tmplData)
}

func templatizeTitle(title string, tmplData interface{}) string {
        var tt bytes.Buffer
        Create := func(name, t string) *template.Template {
                return template.Must(template.New(name).Parse(t))
        }
        titletemp := Create("tt", title)
        titletemp.Execute(&tt, &tmplData)
        return tt.String()
}

// SendCustomToVol renders a custom markdown body and title to a single
// volunteer using the VolShifts data shape. The markdown is parsed as a Go
// template so admins can use {{ .Volunteer.Name }} etc. in the body and title.
func SendCustomToVol(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf, volinfo *types.VolInfo, title, markdown string) ([]byte, error) {
        tmplData := &VolCustom{
                Volunteer:    vol,
                Conf:         conf,
                VolInfo:      volinfo,
                Email:        vol.Email,
                VolShiftLink: helpers.EmailLink(ctx, vol.Email, "/vols/shift"),
        }

        // Build an in-memory Letter so we can reuse the existing renderer pipeline.
        // The UID is used to cache parsed templates by hash; using time.Now() means
        // each send gets its own template entry which is what we want for one-off custom mails.
        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, &tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, vol.Email, letter, renderedTitle, buf)
}

func SendCustomToAttendee(ctx *config.AppContext, reg *types.Registration, conf *types.Conf, title, markdown string) ([]byte, error) {
        tmplData := &struct {
                Conf  *types.Conf
                Email string
        }{
                Conf:  conf,
                Email: reg.Email,
        }

        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, reg.Email, letter, renderedTitle, buf)
}

func SendCustomToApplicant(ctx *config.AppContext, app *types.TalkApp, conf *types.Conf, title, markdown string) ([]byte, error) {
        tmplData := &struct {
                Applicant *types.TalkApp
                Conf      *types.Conf
                Email     string
        }{
                Applicant: app,
                Conf:      conf,
                Email:     app.Email,
        }

        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, &tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, app.Email, letter, renderedTitle, buf)
}

func SendCustomToSpeaker(ctx *config.AppContext, speaker *types.Speaker, conf *types.Conf, talks []*types.Talk, title, markdown string) ([]byte, error) {
        tmplData := &SpeakerCustom{
                Speaker: speaker,
                Conf:    conf,
                Talks:   talks,
                Email:   speaker.Email,
        }

        letter := &mtypes.Letter{
                UID:      uint64(time.Now().UnixNano()),
                Title:    title,
                Markdown: markdown,
        }

        var buf bytes.Buffer
        err := missiveTemplate(ctx, letter).Execute(&buf, &tmplData)
        if err != nil {
                return nil, err
        }

        renderedTitle := templatizeTitle(title, tmplData)
        return sendOnlyFor(ctx, speaker.Email, letter, renderedTitle, buf)
}

func execOnlyFor(ctx *config.AppContext, email, onlyFor string, tmplData interface{}) ([]byte, error) {
        letter, err := getters.GetLetterFor(ctx.Notion, onlyFor)
        if err != nil {
                return nil, err
        }

        /* Execute template for this type */
	var buf bytes.Buffer
	err = missiveTemplate(ctx, letter).Execute(&buf, tmplData)

	if err != nil {
		return nil, err
	}

        /* Also parse/pull the letter title! */
        title := templatizeTitle(letter.Title, tmplData)

        return sendOnlyFor(ctx, email, letter, title, buf)
}

func sendOnlyFor(ctx *config.AppContext, email string, letter *mtypes.Letter, title string, content bytes.Buffer) ([]byte, error) {
	mail := &Mail{
		JobKey:   makeJobKeyRep(email, letter),
		Missive:  letter.Missive(),
		Email:    email,
		Title:    title,
		SendAt:   time.Now(),
		TextBody: content.Bytes(),
	}

        var err error
	mail.HTMLBody, err = BuildHTMLEmail(ctx, content.Bytes())
	if err != nil {
		return nil, err
	}

	ctx.Infos.Printf("Sending (%s)%s to %s at %s", mail.JobKey, title, email, mail.SendAt)

	return mail.HTMLBody, ComposeAndSendMail(ctx, mail)
}
