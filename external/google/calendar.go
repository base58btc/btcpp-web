package google

import (
	"context"
	"log"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	calendar "google.golang.org/api/calendar/v3"

	"github.com/base58btc/btcpp-web/internal/config"
)

var redirectURL = "http://localhost:8888/gcal-callback"
var oauthConfig *oauth2.Config
var calService *calendar.Service 


func InitOauth() {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// Request access to calendar events
	oauthConfig, err = google.ConfigFromJSON(b, calendar.CalendarEventsScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	oauthConfig.RedirectURL = redirectURL
}

func HandleLogin(w http.ResponseWriter, r *http.Request, app *config.AppContext) {
	url := oauthConfig.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusFound)
}

func HandleLoginCallback(w http.ResponseWriter, r *http.Request, app *config.AppContext) bool {
	ctx := context.Background()
	code := r.URL.Query().Get("code")
	token, err := oauthConfig.Exchange(ctx, code)

	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return false
	}

	// Create authenticated calendar service
	client := oauthConfig.Client(ctx, token)
	calService, err = calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		http.Error(w, "Failed to create cal service: "+err.Error(), http.StatusInternalServerError)
		return false
	}

	return true
}

func IsLoggedIn() bool {
	return calService != nil
}

func RunCalendarInvites() error {
	
	// Define the event
	event := &calendar.Event{
		Summary:     "Automated Go Event",
		Location:    "bitcoin++ Riga, Latvia",
		Description: "Your talk is happening now!",
		Start: &calendar.EventDateTime{
			DateTime: "2025-05-20T10:00:00-07:00",
			TimeZone: "America/Chicago",
		},
		End: &calendar.EventDateTime{
			DateTime: "2025-05-20T11:00:00-07:00",
			TimeZone: "America/Chicago",
		},
		Attendees: []*calendar.EventAttendee{
			{Email: "nifty@example.com"},
		},
	}

	// Insert the event into the primary calendar
	_, err := calService.Events.Insert("primary", event).Do()
	return err
}
