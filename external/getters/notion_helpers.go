package getters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	notion "github.com/niftynei/go-notion"
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

func titleValue(content string) *notion.PropertyValue {
	return notion.NewTitlePropertyValue(richTextChunks(content)...)
}

func richTextValue(content string) *notion.PropertyValue {
	return notion.NewRichTextPropertyValue(richTextChunks(content)...)
}

func richTextChunks(content string) []*notion.RichText {
	if content == "" {
		return nil
	}
	pieces := splitForNotion(content)
	out := make([]*notion.RichText, len(pieces))
	for i, p := range pieces {
		out[i] = &notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: p}}
	}
	return out
}

const notionRichTextLimit = 2000

func splitForNotion(s string) []string {
	runes := []rune(s)
	if len(runes) <= notionRichTextLimit {
		return []string{s}
	}
	var out []string
	for len(runes) > notionRichTextLimit {
		out = append(out, string(runes[:notionRichTextLimit]))
		runes = runes[notionRichTextLimit:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func selectValue(name string) *notion.PropertyValue {
	return &notion.PropertyValue{
		Type:   notion.PropertySelect,
		Select: &notion.SelectOption{Name: name},
	}
}

func checkboxValue(b bool) *notion.PropertyValue {
	return &notion.PropertyValue{
		Type:     notion.PropertyCheckbox,
		Checkbox: &b,
	}
}

func multiSelectValue(tags []string) *notion.PropertyValue {
	opts := make([]*notion.SelectOption, len(tags))
	for i, t := range tags {
		opts[i] = &notion.SelectOption{Name: t}
	}
	return &notion.PropertyValue{
		Type:        notion.PropertyMultiSelect,
		MultiSelect: &opts,
	}
}
