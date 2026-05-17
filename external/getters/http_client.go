package getters

import (
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}
