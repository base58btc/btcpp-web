package getters

import (
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func GetVolInfo(ctx *config.AppContext, confRef string) (*types.VolInfo, error) {
	infos, err := GetVolInfos(ctx, confRef)
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, fmt.Errorf("Invalid confref for volinfos %s", confRef)
	}

	return infos[0], nil
}

func GetVolInfoMap(ctx *config.AppContext) (map[string]*types.VolInfo, error) {
	vmap := make(map[string]*types.VolInfo)
	volinfos, err := GetVolInfos(ctx, "")
	if err != nil {
		return vmap, err
	}

	confs, err := FetchConfsCached(ctx)
	if err != nil {
		return vmap, err
	}
	for _, vi := range volinfos {
		for _, conf := range confs {
			if conf.Ref == vi.ConfRef {
				vmap[conf.Tag] = vi
				break
			}
		}
	}

	return vmap, nil
}

func GetVolInfos(ctx *config.AppContext, confRef string) ([]*types.VolInfo, error) {
	if UsePostgresBackend(ctx) {
		return getVolInfosPostgres(ctx, confRef)
	}
	return GetVolInfosNotion(ctx, confRef)
}

func ListVolunteerApps(ctx *config.AppContext, email string) ([]*types.Volunteer, error) {
	if UsePostgresBackend(ctx) {
		return listVolunteerAppsPostgres(ctx, email)
	}
	return ListVolunteerAppsNotion(ctx, email)
}

func FetchVolunteer(ctx *config.AppContext, volRef string) (*types.Volunteer, error) {
	if UsePostgresBackend(ctx) {
		return fetchVolunteerPostgres(ctx, volRef)
	}
	return FetchVolunteerNotion(ctx, volRef)
}

func ListVolunteersForConf(ctx *config.AppContext, confRef string) ([]*types.Volunteer, error) {
	if UsePostgresBackend(ctx) {
		return listVolunteersForConfPostgres(ctx, confRef)
	}
	return ListVolunteersForConfNotion(ctx, confRef)
}
