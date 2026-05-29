package getters

import (
	"sort"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// ListConfInfos fetches every row in ConfInfoDb/conference_days, optionally
// filtered to a single conf by tag.
func ListConfInfos(ctx *config.AppContext, confTag string) ([]*types.ConfInfo, error) {
	if UsePostgresBackend(ctx) {
		return listConfInfosPostgres(ctx, confTag)
	}
	return ListConfInfosNotion(ctx, confTag)
}

// GetConfInfoMap returns a Tag -> []*ConfInfo map, sorted by Day within each
// conf. Convenient for templates that want "the schedule strip for this conf"
// without sifting by tag manually.
func GetConfInfoMap(ctx *config.AppContext) (map[string][]*types.ConfInfo, error) {
	infos, err := ListConfInfos(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]*types.ConfInfo)
	for _, ci := range infos {
		if ci.ConfTag == "" {
			continue
		}
		out[ci.ConfTag] = append(out[ci.ConfTag], ci)
	}
	for tag := range out {
		sort.Slice(out[tag], func(i, j int) bool {
			return out[tag][i].Day < out[tag][j].Day
		})
	}
	return out, nil
}
