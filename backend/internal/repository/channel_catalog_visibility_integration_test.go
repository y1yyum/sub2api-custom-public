//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelCatalogVisibilityPersistence(t *testing.T) {
	ctx := context.Background()
	ch := &service.Channel{}
	repo := newReasoningPricingIntegrationChannel(t, ch)
	pricing := service.ChannelModelPricing{ChannelID: ch.ID, Platform: "openai", Models: []string{"example"}, HideInCatalog: true}
	require.NoError(t, repo.CreateModelPricing(ctx, &pricing))
	assertStored := func(hidden bool) {
		t.Helper()
		rows, err := repo.ListModelPricing(ctx, ch.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, hidden, rows[0].HideInCatalog)
		batch, err := repo.batchLoadModelPricing(ctx, []int64{ch.ID})
		require.NoError(t, err)
		require.Equal(t, hidden, batch[ch.ID][0].HideInCatalog)
	}
	assertStored(true)
	pricing.HideInCatalog = false
	require.NoError(t, repo.UpdateModelPricing(ctx, &pricing))
	assertStored(false)
	pricing.HideInCatalog = true
	require.NoError(t, repo.ReplaceModelPricing(ctx, ch.ID, []service.ChannelModelPricing{pricing}))
	assertStored(true)
}
