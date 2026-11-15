//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelCatalogVisibilityRequestResponse(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		request := []channelModelPricingRequest{{Platform: "openai", Models: []string{"example"}, HideInCatalog: hidden}}
		pricing := pricingRequestToService(request, true)
		require.Equal(t, hidden, pricing[0].HideInCatalog)
		response := pricingToResponse(&pricing[0])
		data, err := json.Marshal(response)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(data, &decoded))
		require.Equal(t, hidden, decoded["hide_in_catalog"])
	}
}
