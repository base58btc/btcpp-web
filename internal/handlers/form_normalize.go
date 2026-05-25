package handlers

import (
	"strings"

	"btcpp-web/internal/types"
)

func trimTalkApp(app *types.TalkApp) {
	if app == nil {
		return
	}
	app.Name = strings.TrimSpace(app.Name)
	app.Phone = strings.TrimSpace(app.Phone)
	app.Email = strings.TrimSpace(app.Email)
	app.Signal = strings.TrimSpace(app.Signal)
	app.Telegram = strings.TrimSpace(app.Telegram)
	app.ContactAt = strings.TrimSpace(app.ContactAt)
	app.Hometown = strings.TrimSpace(app.Hometown)
	app.Twitter = types.ParseTwitter(app.Twitter.Handle)
	app.Nostr = strings.TrimSpace(app.Nostr)
	app.Github = strings.TrimSpace(app.Github)
	app.Website = strings.TrimSpace(app.Website)
	app.Visa = strings.TrimSpace(app.Visa)
	app.Pic = strings.TrimSpace(app.Pic)
	app.NormPhoto = strings.TrimSpace(app.NormPhoto)
	app.Org = strings.TrimSpace(app.Org)
	app.OrgTwitter = types.ParseTwitter(app.OrgTwitter.Handle)
	app.OrgNostr = strings.TrimSpace(app.OrgNostr)
	app.OrgSite = strings.TrimSpace(app.OrgSite)
	app.OrgLogo = strings.TrimSpace(app.OrgLogo)
	app.TalkTitle = strings.TrimSpace(app.TalkTitle)
	app.Description = strings.TrimSpace(app.Description)
	app.PresType = strings.TrimSpace(app.PresType)
	app.Recording = strings.TrimSpace(app.Recording)
	app.Setup = strings.TrimSpace(app.Setup)
	app.DiscoveredVia = strings.TrimSpace(app.DiscoveredVia)
	app.Shirt = strings.TrimSpace(app.Shirt)
	app.Comments = strings.TrimSpace(app.Comments)
}

func trimVolunteer(vol *types.Volunteer) {
	if vol == nil {
		return
	}
	vol.Name = strings.TrimSpace(vol.Name)
	vol.Email = strings.TrimSpace(vol.Email)
	vol.Phone = strings.TrimSpace(vol.Phone)
	vol.Signal = strings.TrimSpace(vol.Signal)
	vol.ContactAt = strings.TrimSpace(vol.ContactAt)
	vol.Comments = strings.TrimSpace(vol.Comments)
	vol.DiscoveredVia = strings.TrimSpace(vol.DiscoveredVia)
	vol.Hometown = strings.TrimSpace(vol.Hometown)
	vol.Twitter = types.ParseTwitter(vol.Twitter.Handle)
	vol.Nostr = strings.TrimSpace(vol.Nostr)
	vol.Shirt = strings.TrimSpace(vol.Shirt)
}

func trimOrg(org *types.Org) {
	if org == nil {
		return
	}
	org.Name = strings.TrimSpace(org.Name)
	org.Tagline = strings.TrimSpace(org.Tagline)
	org.LogoLight = strings.TrimSpace(org.LogoLight)
	org.LogoDark = strings.TrimSpace(org.LogoDark)
	org.Email = strings.TrimSpace(org.Email)
	org.Website = strings.TrimSpace(org.Website)
	org.LinkedIn = strings.TrimSpace(org.LinkedIn)
	org.Instagram = strings.TrimSpace(org.Instagram)
	org.Youtube = strings.TrimSpace(org.Youtube)
	org.Github = strings.TrimSpace(org.Github)
	org.Twitter = types.ParseTwitter(org.Twitter.Handle)
	org.Nostr = strings.TrimSpace(org.Nostr)
	org.Matrix = strings.TrimSpace(org.Matrix)
	org.Notes = strings.TrimSpace(org.Notes)
}
