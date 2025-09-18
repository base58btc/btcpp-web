package handlers

import (
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

type (
	ChargeEvent struct {
		ID          string `schema:"id"`
		Status      string `schema:"status"`
		Description string `schema:"description"`
		HashedOrder string `schema:"hashed_order"`
	}

	Charge struct {
		ID          string                  `json:"id"`
		Status      string                  `json:"status"`
		Description string                  `json:"description"`
		FiatVal     float64                 `json:"fiat_value"`
		Price       int64                   `json:"price"`
		CreatedAt   time.Time               `json:"created_at"`
		Metadata    *types.OpenNodeMetadata `json:"metadata"`
	}

	envelope struct {
		Data Charge `json:"data"`
	}
)

func GetCharge(ctx *config.AppContext, ID string) (*Charge, error) {

	url := fmt.Sprintf("https://api.opennode.com/v2/charge/%s", ID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", ctx.Env.OpenNode.Key)
	req.Header.Set("accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("Failed to fetch, %d", res.StatusCode)
	}

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var envel envelope
	err = json.Unmarshal(resBody, &envel)
	return &envel.Data, err
}
