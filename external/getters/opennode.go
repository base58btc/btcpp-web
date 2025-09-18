package getters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

const CHARGES_ENDPOINT string = "/charges"

func InitOpenNodeCheckout(ctx *config.AppContext, tixPrice uint, tix *types.ConfTicket, conf *types.Conf, isLocal bool, count uint, email string, discountRef string, subNewsletter bool) (*types.OpenNodePayment, error) {

	metadata := &types.OpenNodeMetadata{
		Email:       email,
		Quantity:    float64(count),
		ConfRef:     conf.Ref,
		TixLocal:    isLocal,
		DiscountRef: discountRef,
		/* We have to save it b/c OpenNode doesnt */
		Currency:  tix.Currency,
		Subscribe: subNewsletter,
	}

	domain := ctx.Env.GetURI()
	onReq := &types.OpenNodeRequest{
		Amount:        float64(tixPrice),
		Description:   conf.Desc,
		Currency:      tix.Currency,
		CallbackURL:   domain + "/callback/opennode",
		SuccessURL:    domain + "/conf/" + conf.Tag + "/success",
		AutoSettle:    false,
		TTL:           360,
		Metadata:      metadata,
		NotifEmail:    email,
		CustomerEmail: email,
	}

	if !ctx.Env.Prod {
		onReq.Amount = float64(0.01)
	}

	payload, err := json.Marshal(onReq)
	if err != nil {
		return nil, err
	}

	chargesURL := ctx.Env.OpenNode.Endpoint + CHARGES_ENDPOINT
	req, _ := http.NewRequest("POST", chargesURL, bytes.NewBuffer(payload))
	req.Header.Add("Authorization", ctx.Env.OpenNode.Key)
	req.Header.Add("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error returned from opennode %d: %s", resp.StatusCode, body)
	}

	var onresp types.OpenNodeResponse
	json.Unmarshal(body, &onresp)

	return onresp.Data, nil
}
