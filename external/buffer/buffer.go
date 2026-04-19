package buffer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"
)

const apiURL = "https://api.buffer.com/graphql"

var (
	apiKey   string
	orgID    string
	channels []Channel
	mu       sync.Mutex
	channelsFetchedAt time.Time
)

type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"`
}

type PostResult struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	DueAt string `json:"dueAt"`
}

func Init(key string) {
	apiKey = key
}

func IsConfigured() bool {
	return apiKey != ""
}

type graphqlReq struct {
	Query string `json:"query"`
}

type graphqlResp struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func graphqlRequest(query string) (json.RawMessage, error) {
	body, err := json.Marshal(&graphqlReq{Query: query})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("buffer API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var gResp graphqlResp
	if err := json.Unmarshal(respBody, &gResp); err != nil {
		return nil, fmt.Errorf("failed to parse buffer response: %s", err)
	}

	if len(gResp.Errors) > 0 {
		return nil, fmt.Errorf("buffer API error: %s", gResp.Errors[0].Message)
	}

	return gResp.Data, nil
}

func fetchOrgID() (string, error) {
	if orgID != "" {
		return orgID, nil
	}

	data, err := graphqlRequest(`query { account { organizations { id } } }`)
	if err != nil {
		return "", err
	}

	var result struct {
		Account struct {
			Organizations []struct {
				ID string `json:"id"`
			} `json:"organizations"`
		} `json:"account"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}

	if len(result.Account.Organizations) == 0 {
		return "", fmt.Errorf("no organizations found in Buffer account")
	}

	orgID = result.Account.Organizations[0].ID
	return orgID, nil
}

func FetchChannels() ([]Channel, error) {
	mu.Lock()
	defer mu.Unlock()

	if len(channels) > 0 && time.Since(channelsFetchedAt) < 5*time.Minute {
		return channels, nil
	}

	oid, err := fetchOrgID()
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`query { channels(input: { organizationId: "%s" }) { id name service } }`, oid)
	data, err := graphqlRequest(query)
	if err != nil {
		return nil, err
	}

	var result struct {
		Channels []Channel `json:"channels"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	channels = result.Channels
	channelsFetchedAt = time.Now()
	return channels, nil
}

func CreatePost(channelID, text string, imageURLs []string, service string) (*PostResult, error) {
	textEscaped, _ := json.Marshal(text)

	var assetsBlock string
	if len(imageURLs) > 0 {
		var imgs []string
		for _, u := range imageURLs {
			escaped, _ := json.Marshal(u)
			imgs = append(imgs, fmt.Sprintf(`{ url: %s }`, string(escaped)))
		}
		assetsBlock = fmt.Sprintf(`, assets: { images: [%s] }`, strings.Join(imgs, ", "))
	}

	var metadataBlock string
	if service == "instagram" {
		igType := "post"
		if len(imageURLs) > 1 {
			igType = "carousel"
		}
		metadataBlock = fmt.Sprintf(`, metadata: { instagram: { type: %s, shouldShareToFeed: true } }`, igType)
	}

	query := fmt.Sprintf(`mutation {
		createPost(input: {
			text: %s,
			channelId: "%s",
			schedulingType: automatic,
			mode: addToQueue
			%s
			%s
		}) {
			... on PostActionSuccess {
				post { id text dueAt }
			}
			... on MutationError {
				message
			}
		}
	}`, string(textEscaped), channelID, assetsBlock, metadataBlock)

	data, err := graphqlRequest(query)
	if err != nil {
		return nil, err
	}

	var result struct {
		CreatePost struct {
			Post    *PostResult `json:"post"`
			Message string      `json:"message"`
		} `json:"createPost"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	if result.CreatePost.Message != "" {
		return nil, fmt.Errorf("buffer post error: %s", result.CreatePost.Message)
	}

	return result.CreatePost.Post, nil
}
