package helpers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
        "net/url"
	"os"
	"sort"
        "strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

func CurrentYear() uint {
	year, _, _ := time.Now().Date()
	return uint(year)
}

func MakeDir(dirpath string) error {
	if _, err := os.Stat(dirpath); os.IsNotExist(err) {
		return os.MkdirAll(dirpath, os.ModePerm)
	}

	return nil
}

func GetOtherConfs(confs []*types.Conf, conf types.Conf) []types.CheckItem {
        items := make([]types.CheckItem, 0)
        for _, c := range confs {
                if !c.Active || !c.InFuture() {
                        continue
                }

                /* Filter out this specific event */
                if c.Ref == conf.Ref {
                        continue
                }

                items = append(items, types.CheckItem{
                        ItemID: "conf-" + c.Ref,
                        ItemDesc: c.Desc + " " + c.DateDesc,
                })
        }

        return items
}

func BuildJobs(prefix string, jobs []*types.JobType, inclWild bool) ([]types.CheckItem) {
        joblist := make([]types.CheckItem, 0)
        for _, j := range jobs {
                if !j.Show || j.IsWildcard() && !inclWild {
                                continue
                }

                joblist = append(joblist, types.CheckItem{
                        ItemID: prefix + j.Tag,
                        ItemDesc: j.Title,
                        Checked: j.IsWildcard(),
                })
        }
        return joblist
}

func GetPresentationTypes() ([]types.CheckItem) {
        return []types.CheckItem {
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "lntalk",
                        ItemDesc: "5min lightning talk",
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "20talk",
                        ItemDesc: "20m talk",
                        Checked: true,
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "30talk",
                        ItemDesc: "30m talk",
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "45panel",
                        ItemDesc: "45m panel",
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "45workshop",
                        ItemDesc: "45m workshop",
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "60workshop",
                        ItemDesc: "60m workshop",
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "90workshop",
                        ItemDesc: "90m workshop",
                },
                types.CheckItem{
                        Group: "PresType",
                        ItemID: "120workshop",
                        ItemDesc: "2h workshop",
                },
        }
}

// GetRecordingOptions returns the radio options for the Recording field on
// the speaker application. Values match the Notion select options on both the
// TalkApp and Talks DBs; descriptions are the user-facing labels.
func GetRecordingOptions() []types.CheckItem {
        return []types.CheckItem{
                {
                        Group:    "Recording",
                        ItemID:   "RecordingOK",
                        ItemDesc: "Recording Ok",
                        Checked:  true,
                },
                {
                        Group:    "Recording",
                        ItemID:   "NoRecord",
                        ItemDesc: "Do Not Record",
                },
                {
                        Group:    "Recording",
                        ItemID:   "AudioOnly",
                        ItemDesc: "Audio Only (Don't Show My Face)",
                },
        }
}

func ParsePresentationType(prefix string, form url.Values) string {
        for k, _ := range form { 
                if strings.HasPrefix(k, prefix) {
                        return form.Get(k)
                }
        }
        return ""
}

func ParseFormJobs(prefix string, form url.Values, jobs []*types.JobType) ([]*types.JobType) {
        joblist := make([]*types.JobType, 0)

        for k, _ := range form { 
                if strings.HasPrefix(k, prefix) {
                        for _, j := range jobs {
                                if j.Tag == k[len(prefix):] {
                                        joblist = append(joblist, j)
                                }
                        }
                }
        }
        return joblist
}

func ParseFormConfs(prefix string, form url.Values, confs []*types.Conf) ([]*types.Conf) {
        conflist := make([]*types.Conf, 0)

        for k, _ := range form { 
                if strings.HasPrefix(k, prefix) {
                        conf := FindConfByRef(confs, k[len(prefix):])
                        if conf == nil {
                                continue
                        }
                        conflist = append(conflist, conf)
                }
        }
        return conflist
}


func FindConfByRef(confs []*types.Conf, confRef string) *types.Conf {
	for _, conf := range confs {
		if conf.Ref == confRef {
			return conf
		}
	}
	return nil
}

func ConfTagSet(confs []*types.Conf) map[string]*types.Conf {
        confset := make(map[string]*types.Conf)
        for _, conf := range confs {
                confset[conf.Tag] = conf
        }
        return confset
}

func HotelsForConf(ctx *config.AppContext, conf *types.Conf) []*types.Hotel {
	hotels := make([]*types.Hotel, 0)
	allhotels, err := getters.FetchHotelsCached(ctx)
	if err != nil {
		ctx.Err.Printf("error fetching hotels: %s", err)
		return nil
	}
	for _, hotel := range allhotels {
		if hotel.ConfRef == conf.Ref {
			hotels = append(hotels, hotel)
		}
	}
	// Sort by the Order field (smaller first). Stable sort so two
	// hotels at the same Order value keep their cache-arrival
	// order — admins can disambiguate by editing one of them.
	sort.SliceStable(hotels, func(i, j int) bool {
		return hotels[i].Order < hotels[j].Order
	})
	return hotels
}

func FindConf(r *http.Request, app *config.AppContext) (*types.Conf, error) {
	params := mux.Vars(r)
	confTag := params["conf"]

	confs, err := getters.FetchConfsCached(app)
	if err != nil {
		return nil, err
	}
	for _, conf := range confs {
		if conf.Tag == confTag {
			return conf, nil
		}
	}

	return nil, fmt.Errorf("'%s' not found (url: %s)", confTag, r.URL.String())
}

func MiniCss() string {
	css, err := ioutil.ReadFile("static/css/mini.css")
	if err != nil {
		panic(err)
	}
	return string(css)
}

func GetSubscribeToken(sec []byte, email, newsletter string, timestamp uint64) (string, string) {
	/* Make a lil hash using the email + timestamp + newsletter */
	h := sha256.New()
	h.Write(sec)
	h.Write([]byte(email))
	h.Write([]byte(newsletter))
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, timestamp)
	h.Write(b)

	/* Token is 8-bytes hash prefix, hex of email,
	 * hex of newsletter, hex of timestamp
	 */

	hashB := h.Sum(nil)
	hash := hex.EncodeToString(hashB[:8])
	emailHex := hex.EncodeToString([]byte(email))
	subHex := hex.EncodeToString([]byte(newsletter))
	timeHex := hex.EncodeToString(b)
	return hash, fmt.Sprintf("%s-%s-%s-%s", hash, emailHex, subHex, timeHex)
}

func GetSessionKey(p string, r *http.Request) (string, bool) {
	ok := r.URL.Query().Has(p)
	key := r.URL.Query().Get(p)
	return key, ok
}

func MakeJobHash(email string, uid uint64, title string) string {
	h := sha256.New()
	h.Write([]byte(email))
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uid)
	h.Write(b)
	h.Write([]byte(title))
	return hex.EncodeToString(h.Sum(nil))
}

// CheckPin / Render401 used to gate every admin handler on a single
// shared PIN held in session storage. Replaced by the role-aware
// auth.RequireRole flow — see internal/auth and internal/handlers/auth_shim.go.
// Removed in favor of redirect-to-/login on missing identity.

func VerifyEmailHMAC(ctx *config.AppContext, hmac, email string) bool {
        verify := CreateEmailHMAC(ctx, email)
        return verify == hmac
}

func CreateEmailHMAC(ctx *config.AppContext, email string) string {
	mac := hmac.New(sha256.New, ctx.Env.HMACKey[:])
	mac.Write([]byte(email))
	return hex.EncodeToString(mac.Sum(nil))
}

// MintInviteToken returns a fresh URL-safe random token for a co-speaker
// invite link. Stored on the proposal so the link can be revoked by
// rotating the field. 16 bytes → 22 chars base64url, plenty of entropy
// for an unguessable share link.
func MintInviteToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure on a healthy box is exceptional; log and
		// surface rather than silently issue a weak token.
		panic(fmt.Sprintf("MintInviteToken: rand.Read: %s", err))
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// InviteLink builds the full shareable URL for a proposal's current
// InviteToken. Returns "" when the proposal has no token (i.e., link
// has been revoked).
func InviteLink(ctx *config.AppContext, proposalID, inviteToken string) string {
	if inviteToken == "" {
		return ""
	}
	u, err := url.Parse(ctx.Env.GetURI())
	if err != nil {
		return ""
	}
	u.Path = "/invite-speaker/" + proposalID
	q := u.Query()
	q.Set("t", inviteToken)
	u.RawQuery = q.Encode()
	return u.String()
}

func VolShiftLink(ctx *config.AppContext, vol *types.Volunteer) string {
        return EmailLink(ctx, vol.Email, "/vols/shift")
}

func EmailLink(ctx *config.AppContext, email, path string) string {
        u, err := url.Parse(ctx.Env.GetURI())
        if err != nil {
                return ""
        }
        u.Path = path
        hmac := CreateEmailHMAC(ctx, email)
        encodedHMAC := base64.RawURLEncoding.EncodeToString([]byte(hmac))
        encodedEmail := base64.RawURLEncoding.EncodeToString([]byte(email))
        q := u.Query()
        q.Set("hr", encodedHMAC)
        q.Set("em", encodedEmail)
        u.RawQuery = q.Encode()
        return u.String()
}

func SpeakerTalks(speaker *types.Speaker, talks []*types.Talk) []*types.Talk {
        st := make([]*types.Talk, 0)
        for _, talk := range talks {
                for _, sp := range talk.Speakers {
                        if speaker.ID == sp.ID {
                                st = append(st, talk)
                                break
                        }
                }
        }

        return st
}

func SponsorSocialPostRef(confTag, sponsorID string) string {
        return fmt.Sprintf("%s-%s", confTag, sponsorID)
}

func SpeakerSocialPostRef(confTag, talkID, speakerID string) string {
        return fmt.Sprintf("%s-%s-%s", confTag, talkID, speakerID)
}

func TalkSocialPostRef(confTag, talkID string) string {
        return fmt.Sprintf("%s-%s", confTag, talkID)
}
