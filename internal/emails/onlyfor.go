package emails

import (
        "bytes"
        "fmt"
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

        return sendOnlyFor(ctx, email, letter, buf)
}

func sendOnlyFor(ctx *config.AppContext, email string, letter *mtypes.Letter, content bytes.Buffer) ([]byte, error) {
	mail := &Mail{
		JobKey:   makeJobKeyRep(email, letter),
		Missive:  letter.Missive(),
		Email:    email,
		Title:    letter.Title,
		SendAt:   time.Now(),
		TextBody: content.Bytes(),
	}

        var err error
	mail.HTMLBody, err = BuildHTMLEmail(ctx, content.Bytes())
	if err != nil {
		return nil, err
	}

	ctx.Infos.Printf("Sending (%s)%s to %s at %s", mail.JobKey, letter.Title, email, mail.SendAt)

	return mail.HTMLBody, ComposeAndSendMail(ctx, mail)
}
