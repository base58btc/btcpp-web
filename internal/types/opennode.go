package types

type (
	OpenNodeConfig struct {
		Key      string
		Endpoint string
	}

	OpenNodeRequest struct {
		Amount        float64           `json:"amount"`
		Description   string            `json:"description"`
		Currency      string            `json:"currency"`
		CustomerEmail string            `json:"customer_email"`
		NotifEmail    string            `json:"notif_email"`
		CustomerName  string            `json:"customer_name"`
		OrderID       string            `json:"order_id"`
		CallbackURL   string            `json:"callback_url"`
		SuccessURL    string            `json:"success_url"`
		AutoSettle    bool              `json:"auto_settle"`
		TTL           uint              `json:"ttl"`
		Metadata      *OpenNodeMetadata `json:"metadata"`
	}

	OpenNodeMetadata struct {
		Email       string  `json:"email"`
		Quantity    float64 `json:"quantity"`
		ConfRef     string  `json:"conf-ref"`
		TixLocal    bool    `json:"tix-local"`
		DiscountRef string  `json:"discount,omitempty"`
		Currency    string  `json:"currency"`
		Subscribe   bool    `json:"subscribe,omitempty"`
	}

	OpenNodeChainInvoice struct {
		BTCAddress string `json:"address"`
	}

	OpenNodeLightningInvoice struct {
		ExpiresAt uint64 `json:"expires_at"`
		Invoice   string `json:"payreq"`
	}

	OpenNodePayment struct {
		ID                string
		Description       string
		DescHash          string `json:"desc_hash"`
		CreatedAt         uint64 `json:"created_at"`
		Status            string
		Amount            uint64
		CallbackURL       string `json:"callback_url"`
		SuccessURL        string `json:"success_url"`
		HostedCheckoutURL string `json:"hosted_checkout_url"`
		OrderID           string `json:"order_id"`
		Currency          string
		SourceFiatValue   float64 `json:"source_fiat_value"`
		AutoSettle        bool    `json:"auto_settle"`
		NotifEmail        string  `json:"notif_email"`
		BTCAddress        string  `json:"address"`
		Metadata          map[string]string
		ChainInvoice      OpenNodeChainInvoice     `json:"chain_invoice"`
		URI               string                   `json:"uri"`
		TTL               string                   `json:"ttl"`
		LNInvoice         OpenNodeLightningInvoice `json:"lightning_invoice"`
	}

	OpenNodeResponse struct {
		Data *OpenNodePayment
	}
)
