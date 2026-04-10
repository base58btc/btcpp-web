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

        VolShifts struct {
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

func execOnlyFor(ctx *config.AppContext, email, onlyFor string, tmplData interface{}) ([]byte, error) {
        letter, err := getters.GetLetterFor(ctx.Notion, onlyFor)
        if err != nil {
                return nil, err
        }

        /* Execute template for this type */
	var buf bytes.Buffer
	err = missiveTemplate(ctx, letter).Execute(&buf, &tmplData)

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
