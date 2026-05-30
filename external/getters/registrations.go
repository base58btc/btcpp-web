package getters

import (
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func CheckIn(ctx *config.AppContext, ticket string) (string, bool, error) {
	if UsePostgresBackend(ctx) {
		return checkInPostgres(ctx, ticket)
	}
	return CheckInNotion(ctx.Notion, ticket)
}

func SoldTixCached(ctx *config.AppContext, conf *types.Conf) uint {
	/* update the sold tix cache every time */
	go UpdateSoldTix(ctx, conf)

	return conf.TixSold
}

func UpdateSoldTix(ctx *config.AppContext, conf *types.Conf) {
	soldTixCount, err := SoldTixCount(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("error fetching sold tix %s %s", conf.Ref, err)
	} else {
		ctx.Infos.Printf("Loaded sold tix count %s %d!", conf.Ref, soldTixCount)
		conf.TixSold = soldTixCount
	}
}

func SoldTixCount(ctx *config.AppContext, confRef string) (uint, error) {
	if UsePostgresBackend(ctx) {
		return soldTixCountPostgres(ctx, confRef)
	}
	return SoldTixCountNotion(ctx.Notion, confRef)
}

func FetchRegistrations(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
	if UsePostgresBackend(ctx) {
		return fetchRegistrationsPostgres(ctx, confRef)
	}
	return FetchRegistrationsNotion(ctx, confRef)
}

func ListRegistrationsByEmail(ctx *config.AppContext, email string) ([]*types.Registration, error) {
	if UsePostgresBackend(ctx) {
		return listRegistrationsByEmailPostgres(ctx, email)
	}
	return ListRegistrationsByEmailNotion(ctx, email)
}

// EmailHasRegistration reports whether the email appears at all in the
// registration rows. Used by the talk-apply form to hide the "first bitcoin++"
// checkbox for returning attendees.
func EmailHasRegistration(ctx *config.AppContext, email string) (bool, error) {
	regs, err := ListRegistrationsByEmail(ctx, email)
	if err != nil {
		return false, err
	}
	return len(regs) > 0, nil
}

func ticketMatch(tickets []string, rez *types.Registration) bool {
	for _, tix := range tickets {
		if strings.Contains(rez.ItemBought, tix) {
			return true
		}
	}

	return false
}

func checkActive(ctx *config.AppContext, confRef string) bool {
	confs, err := FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("couldn't fetch confs?? %s", err)
		return false
	}

	for _, conf := range confs {
		if confRef == conf.Ref {
			return conf.Active
		}
	}

	return false
}

func FetchRegistrationsConf(ctx *config.AppContext, confRef string) ([]*types.Registration, error) {
	return FetchRegistrations(ctx, confRef)
}

func FetchBtcppRegistrations(ctx *config.AppContext, activeOnly bool) ([]*types.Registration, error) {
	var btcppres []*types.Registration
	rezzies, err := FetchRegistrations(ctx, "")

	if err != nil {
		return nil, err
	}

	for _, r := range rezzies {
		if r.RefID == "" {
			continue
		}

		if activeOnly && !checkActive(ctx, r.ConfRef) {
			continue
		}

		btcppres = append(btcppres, r)
	}

	return btcppres, nil
}

func AddTickets(ctx *config.AppContext, entry *types.Entry, src string) error {
	if UsePostgresBackend(ctx) {
		return addTicketsPostgres(ctx, entry, src)
	}
	return addTicketsNotion(ctx.Notion, entry, src)
}

func RevokeTicket(ctx *config.AppContext, lookupID string) error {
	if UsePostgresBackend(ctx) {
		return revokeTicketPostgres(ctx, lookupID)
	}
	return revokeTicketNotion(ctx.Notion, lookupID)
}
