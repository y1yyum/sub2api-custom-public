//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type catalogVisibilityStatusStub struct{}

func (catalogVisibilityStatusStub) Matrix(context.Context, ChannelMonitorV2Filter, ChannelMonitorV2GroupBy, bool) (*ChannelMonitorV2Matrix, error) {
	return &ChannelMonitorV2Matrix{}, nil
}

func (catalogVisibilityStatusStub) RecentCalls(context.Context, ChannelMonitorV2Filter, int) (*ChannelMonitorV2RecentCalls, error) {
	return &ChannelMonitorV2RecentCalls{}, nil
}

func TestCatalogVisibilityFiltersPricingAndAliases(t *testing.T) {
	ctx := context.Background()
	ch := plazaPricedChannel(1, "channel", []int64{10}, "openai", "hidden")
	ch.ModelPricing[0].HideInCatalog = true
	ch.ModelMapping = map[string]map[string]string{"openai": {"alias": "hidden", "unpriced": "unpriced"}}
	ch.ModelPricing = append(ch.ModelPricing, ChannelModelPricing{Platform: "openai", Models: []string{"visible"}})
	plaza := newPlazaService([]Channel{ch}, []Group{{ID: 10, Platform: "openai", RateMultiplier: 1}}, nil)
	groups, err := plaza.ListCatalogGroups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 2)
	require.Equal(t, "unpriced", groups[0].Models[0].Name)
	require.Equal(t, "visible", groups[0].Models[1].Name)

	all, err := plaza.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, all[0].Models, 4)
	require.Len(t, ch.SupportedModels(), 4)
	require.NotNil(t, ch.GetModelPricing("hidden"))

	catalog := newChannelStatusCatalogService(plaza, catalogVisibilityStatusStub{}, nil)
	query := CatalogQuery{Filter: ChannelMonitorV2Filter{RestrictGroups: true, AllowedGroupIDs: []int64{10}}}
	rows, err := catalog.Catalog(ctx, query)
	require.NoError(t, err)
	require.Len(t, rows.Groups[0].Models, 2)
	_, err = catalog.Details(ctx, query, NewCatalogRowKey(10, "openai", "hidden"))
	require.ErrorIs(t, err, ErrCatalogRowNotFound)
}

func TestCatalogVisibilityFiltersBeforeChannelDedup(t *testing.T) {
	ctx := context.Background()
	hidden := plazaPricedChannel(1, "a-hidden", []int64{10}, "openai", "shared")
	hidden.ModelPricing[0].HideInCatalog = true
	visible := plazaPricedChannel(2, "b-visible", []int64{10}, "openai", "shared")
	visible.ModelPricing[0].InputPrice = testPtrFloat64(8e-6)
	groups := []Group{{ID: 10, Platform: "openai"}}
	for _, channels := range [][]Channel{{hidden}, {hidden, visible}} {
		plaza := newPlazaService(channels, groups, nil)
		result, err := plaza.ListCatalogGroups(ctx)
		require.NoError(t, err)
		if len(channels) == 1 {
			require.Empty(t, result)
		} else {
			require.Len(t, result, 1)
			require.Len(t, result[0].Models, 1)
			require.Equal(t, 8e-6, *result[0].Models[0].Pricing.InputPrice)
		}
	}
}

func TestCatalogVisibilityAliasUsesOwnPricingSwitch(t *testing.T) {
	for _, hideAlias := range []bool{false, true} {
		ch := plazaPricedChannel(1, "channel", []int64{10}, "openai", "target")
		ch.ModelPricing[0].HideInCatalog = !hideAlias
		ch.ModelPricing = append(ch.ModelPricing, ChannelModelPricing{
			Platform: "openai", Models: []string{"alias"}, HideInCatalog: hideAlias,
		})
		ch.ModelMapping = map[string]map[string]string{"openai": {"alias": "target"}}
		plaza := newPlazaService([]Channel{ch}, []Group{{ID: 10, Platform: "openai"}}, nil)
		groups, err := plaza.ListCatalogGroups(context.Background())
		require.NoError(t, err)
		require.Len(t, groups, 1)
		require.Len(t, groups[0].Models, 1)
		if hideAlias {
			require.Equal(t, "target", groups[0].Models[0].Name)
		} else {
			require.Equal(t, "alias", groups[0].Models[0].Name)
			require.Equal(t, 3e-6, *groups[0].Models[0].Pricing.InputPrice)
		}
	}
}
