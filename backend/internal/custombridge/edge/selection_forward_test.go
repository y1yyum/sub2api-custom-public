package edge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/custom/dispatch"
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
)

func TestEdgeSelectionSeedPreservesSignedForwarding(t *testing.T) {
	for _, strict := range []bool{false, true} {
		mode := "shadow"
		if strict {
			mode = "strict"
		}
		t.Run(mode, func(t *testing.T) {
			verifier := fixtureVerifier(t)
			var sent atomic.Int64
			envelopes := make(chan custombridge.ForwardEnvelope, 5)
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				envelope, err := verifier.Verify(r, body, custombridge.ForwardPrincipal{InternalKeyID: 101, GroupID: 7}, time.Now())
				if err != nil || string(body) != strictBody || r.Header.Get("Authorization") != "Bearer "+testCoreKey ||
					r.Header.Get("X-Account-ID") != "" || r.Header.Get("X-Region") != "" || r.Header.Get("X-Selection-Seed") != "" {
					t.Errorf("selection identity changed trusted forwarding: %v", err)
					w.WriteHeader(401)
					return
				}
				envelopes <- envelope
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(429)
				_, _ = io.WriteString(w, `{"error":"synthetic-core"}`)
			}))
			defer core.Close()
			cfg := selectionModeConfig(t, core.URL, strict)
			settings := selectionSettings(&cfg, strict)
			settings.SelectionSeed = &dispatch.SelectionSeed{GatewayInstance: "gateway-frozen", ShardRef: "shard-1"}
			now := time.Now()
			snapshot := shadowSnapshot(now)
			snapshot.Candidates[0].BaseWeight, snapshot.Candidates[0].EffectiveWeight = 10, 3
			second := snapshot.Candidates[0]
			second.Ref, second.CredentialRef, second.AccountID, second.EffectiveWeight = "candidate-b", "credential-b", 8, 7
			snapshot.Candidates = append(snapshot.Candidates, second)
			if strict {
				cfg.Grants[0].StrictTargets[0].MaxInflight = 10
				permit := cfg.Grants[0].StrictTargets[0]
				permit.CandidateRef, permit.AccountID, permit.CredentialRef = second.Ref, second.AccountID, second.CredentialRef
				cfg.Grants[0].StrictTargets = append(cfg.Grants[0].StrictTargets, permit)
			}
			writeShadowSnapshot(t, settings.SnapshotFile, snapshot)
			oracle := dispatch.NewSelectorWithSeed(settings.CoreInstance, *settings.SelectionSeed)
			if err := oracle.Update(snapshot, now); err != nil {
				t.Fatal(err)
			}
			permits, _ := dispatch.NewPermitSet(cfg.Grants[0].StrictTargets)
			handler, server := newTestEdge(t, cfg)
			if strict {
				handler.strict.cache.close()
			} else {
				handler.shadow.close()
			}
			settings.SelectionSeed.GatewayInstance, settings.SelectionSeed.ShardRef = "mutated", "mutated"
			route := dispatch.Route{PoolRef: "pool-fixture", PolicyRevision: "policy-1", Model: "fixture-model", Region: "region-a"}
			for i := range 5 {
				want := int64(42)
				if strict {
					decision, release := oracle.Acquire(route, permits, now)
					if release == nil {
						t.Fatal("synthetic permit set unexpectedly empty")
					}
					want = decision.AccountID
					release()
				}
				response := edgePost(t, server.URL, strictBody, http.Header{
					"Idempotency-Key": {"seed-case-" + strconv.Itoa(i)},
					"X-Account-ID":    {"999"}, "X-Region": {"other-region"}, "X-Selection-Seed": {"client-forgery"},
				})
				if body := readBody(t, response); response.StatusCode != 429 || body != `{"error":"synthetic-core"}` || response.Header.Get("Retry-After") != "3" {
					t.Fatal("selection identity changed the Core response")
				}
				select {
				case envelope := <-envelopes:
					if envelope.AccountID != want || envelope.PoolRef != route.PoolRef || envelope.PolicyRevision != route.PolicyRevision ||
						envelope.CanonicalModel != route.Model || envelope.InternalKeyID != 101 || envelope.GroupID != 7 ||
						envelope.PayerRef != "payer-fixture" || envelope.ActorRef != "actor-fixture" {
						t.Fatalf("signed target differs from frozen local selection: %+v", envelope)
					}
				default:
					t.Fatal("request did not reach the synthetic Core")
				}
			}
			if sent.Load() != 5 {
				t.Fatal("selection performed additional HTTP requests")
			}
			if strict {
				stats := handler.StrictStats()
				if stats.Selected != 5 || stats.Inflight != 0 || stats.Released != 5 || handler.ShadowStats() != (ShadowStats{}) {
					t.Fatal("strict selection changed local slot accounting")
				}
			} else if stats := handler.ShadowStats(); stats.Mismatches != 5 || stats.Observations != 5 {
				t.Fatal("shadow selection changed fixed-account observation")
			}
		})
	}
}
