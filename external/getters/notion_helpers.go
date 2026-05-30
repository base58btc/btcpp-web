package getters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// notionPagePost sends a JSON request directly to Notion's pages API. method
// is "POST" for create, "PATCH" for update. urlPath is appended to the v1/pages
// base.
func notionPagePost(token, method, urlPath string, body map[string]interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, "https://api.notion.com/v1/pages"+urlPath, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion API error: %v", errResp)
	}
	return nil
}

// clearRelationProperty makes a direct HTTP request to Notion API to clear a
// relation. This avoids go-notion omitting empty relation slices from JSON.
func clearRelationProperty(token, pageID, propertyName string) error {
	payload := map[string]interface{}{
		"properties": map[string]interface{}{
			propertyName: map[string]interface{}{
				"relation": []interface{}{},
			},
		},
	}

	return notionPagePost(token, "PATCH", "/"+pageID, payload)
}
