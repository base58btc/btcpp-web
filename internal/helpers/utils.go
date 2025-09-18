package helpers

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
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

func FindConfByRef(confs []*types.Conf, confRef string) *types.Conf {
	for _, conf := range confs {
		if conf.Ref == confRef {
			return conf
		}
	}
	return nil
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

func Render401(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	err := ctx.TemplateCache.ExecuteTemplate(w, "401.tmpl", &LoginPage{
		Year:        CurrentYear(),
		Destination: r.URL.Path,
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/401.tmpl exec template failed %s\n", err.Error())
		return
	}
}

func CheckPin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) bool {
	pin := ctx.Session.GetString(r.Context(), "pin")
	if pin == "" {
		w.Header().Set("x-missing-field", "pin")
		w.WriteHeader(http.StatusUnauthorized)
		ctx.Infos.Printf("401 login failed: %s", r.URL.Path)
		return false
	}
	return pin == ctx.Env.RegistryPin
}
