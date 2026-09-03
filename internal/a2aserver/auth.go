package a2aserver

import (
	"context"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-evm-agent/internal/agenttools"
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/auth"
)

// AuthResolver maps an A2A request onto the authenticated tenant context. It owns no verification logic — challenges, signatures,
// and bearer minting live in the auth_challenge / auth_verify tools — it only
// resolves an already-minted bearer to its tenant and stamps the context.
type AuthResolver struct {
	Tenants *auth.DynamicTenantStore
}

// Attach returns ctx annotated for the tool handlers:
//
//   - Bearer is resolved from the Authorization header, then the envelope
//     field. A resolved bearer becomes a tenant; an unknown or expired one is
//     absent, and the gated handler refuses with its own message.
func (r *AuthResolver) Attach(ctx context.Context, execCtx *a2asrv.ExecutorContext, req *Request) context.Context {
	bearer := bearerFromHeader(execCtx)
	if bearer == "" {
		bearer = req.Bearer
	}
	if bearer == "" || r.Tenants == nil {
		return ctx
	}
	rec, err := r.Tenants.LookupByBearer(bearer)
	if err != nil {
		return ctx
	}
	return agenttools.WithTenant(ctx, agenttools.Tenant{ID: rec.TenantID, Owner: rec.Owner})
}

func bearerFromHeader(execCtx *a2asrv.ExecutorContext) string {
	v := headerValue(execCtx, "authorization")
	if v == "" {
		return ""
	}
	const prefix = "bearer "
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return strings.TrimSpace(v[len(prefix):])
	}
	return ""
}

func headerValue(execCtx *a2asrv.ExecutorContext, key string) string {
	if execCtx == nil || execCtx.ServiceParams == nil {
		return ""
	}
	vals, ok := execCtx.ServiceParams.Get(key)
	if !ok || len(vals) == 0 {
		return ""
	}
	return vals[0]
}
