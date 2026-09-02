// Package agentchain is the client side of this agent's x/agent registry
// lifecycle: query what the chain currently holds for an agent, and land a
// signed registration or update.
//
// It is a LOCAL client, run by the operator from their own machine, not
// something the deployed agent does. That is a change from the flow this
// replaces. Registration used to be self-registration — the agent held an
// operator key on the remote and signed its own MsgRegisterAgent, so the tool
// was an A2A client that authenticated to the running agent and asked it to
// register itself. A caller-signed agent has no key to do that with, so the
// transaction is built and signed here, against the owner key, and the agent's
// only part is serving the card that gets hashed into it.
//
// What still cannot move is the card hash. The value published on chain is the
// sha256 of the card as SERVED, so registering requires a running agent
// answering at its public URL — see cmd/agent-register, which fetches it.
package agentchain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	cmttypes "github.com/cometbft/cometbft/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/chain"
	"github.com/svpchain/svpchain-evm-agent/internal/mcp/mcpcodec"

	agenttypes "github.com/dydxprotocol/v4-chain/protocol/x/agent/types"
)

// Client bundles the three chain surfaces a registration needs: the x/agent
// registry to read current state, x/auth for the signer's account number and
// sequence, and the tx service to broadcast.
type Client struct {
	conn      *grpc.ClientConn
	agents    agenttypes.QueryClient
	accounts  chain.AccountClient
	broadcast chain.BroadcastClient
	rpc       *rpchttp.HTTP
	registry  codectypes.InterfaceRegistry
}

// Dial connects to the chain carrying x/agent. In a single-chain deployment
// that is the same gRPC endpoint the agent serves its own queries from.
func Dial(ctx context.Context, grpcAddr string) (*Client, error) {
	conn, err := chain.Dial(ctx, grpcAddr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", grpcAddr, err)
	}
	// The registry unpacks Any-wrapped accounts and pubkeys; mcpcodec is the
	// one that already knows every svpchain module type plus eth_secp256k1.
	enc := mcpcodec.GetEncodingConfig()
	return &Client{
		conn:      conn,
		agents:    agenttypes.NewQueryClient(conn),
		accounts:  chain.NewAccountClient(conn, enc.InterfaceRegistry),
		broadcast: chain.NewBroadcastClient(conn),
		registry:  enc.InterfaceRegistry,
	}, nil
}

// DialRPC uses a CometBFT RPC endpoint for ABCI queries and broadcast_tx_sync.
// Public RPC domains commonly expose this while keeping gRPC private.
func DialRPC(_ context.Context, rpcURL string) (*Client, error) {
	rpc, err := rpchttp.New(rpcURL, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("cometbft.New %s: %w", rpcURL, err)
	}
	return &Client{rpc: rpc, registry: mcpcodec.GetEncodingConfig().InterfaceRegistry}, nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Params returns the module's registration fee and minimum bond. The bond
// matters at registration time: MsgRegisterAgent carries an explicit
// InitialBond and the handler rejects anything under MinBond, so an operator
// who did not name one needs this to fill it in.
func (c *Client) Params(ctx context.Context) (agenttypes.Params, error) {
	if c.rpc != nil {
		var resp agenttypes.QueryParamsResponse
		if err := c.rpcQuery(ctx, "/dydxprotocol.agent.Query/Params", &agenttypes.QueryParams{}, &resp); err != nil {
			return agenttypes.Params{}, err
		}
		return resp.Params, nil
	}
	resp, err := c.agents.Params(ctx, &agenttypes.QueryParams{})
	if err != nil {
		return agenttypes.Params{}, fmt.Errorf("agent.Query/Params: %w", err)
	}
	return resp.Params, nil
}

// AgentByID looks up a registration. A missing agent is (nil, false, nil)
// rather than an error: "not registered yet" is the ordinary starting state of
// the thing this package exists to fix, not a fault.
func (c *Client) AgentByID(ctx context.Context, agentID string) (*agenttypes.Agent, bool, error) {
	if c.rpc != nil {
		var resp agenttypes.QueryAgentResponse
		err := c.rpcQuery(ctx, "/dydxprotocol.agent.Query/Agent", &agenttypes.QueryAgent{AgentId: agentID}, &resp)
		if err != nil {
			if isRPCNotFound(err) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("agent.Query/Agent %s: %w", agentID, err)
		}
		return &resp.Agent, true, nil
	}
	resp, err := c.agents.Agent(ctx, &agenttypes.QueryAgent{AgentId: agentID})
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("agent.Query/Agent %s: %w", agentID, err)
	}
	return &resp.Agent, true, nil
}

// Account returns the signer's account number and sequence.
func (c *Client) Account(ctx context.Context, address string) (chain.AccountInfo, error) {
	if c.rpc != nil {
		var resp authtypes.QueryAccountResponse
		if err := c.rpcQuery(ctx, "/cosmos.auth.v1beta1.Query/Account", &authtypes.QueryAccountRequest{Address: address}, &resp); err != nil {
			return chain.AccountInfo{}, fmt.Errorf("auth.Query/Account %s: %w", address, err)
		}
		if resp.Account == nil {
			return chain.AccountInfo{}, fmt.Errorf("auth.Query/Account %s: empty account", address)
		}
		var acc sdk.AccountI
		if err := c.registry.UnpackAny(resp.Account, &acc); err != nil {
			return chain.AccountInfo{}, fmt.Errorf("unpack account %s: %w", address, err)
		}
		return chain.AccountInfo{AccountNumber: acc.GetAccountNumber(), Sequence: acc.GetSequence()}, nil
	}
	return c.accounts.Account(ctx, address)
}

// BroadcastSync submits signed tx bytes and turns a non-zero CheckTx code into
// an error, so callers do not have to inspect the result to know it failed.
func (c *Client) BroadcastSync(ctx context.Context, txBytes []byte) (chain.BroadcastResult, error) {
	if c.rpc != nil {
		resp, err := c.rpc.BroadcastTxSync(ctx, cmttypes.Tx(txBytes))
		if err != nil {
			return chain.BroadcastResult{}, fmt.Errorf("cometbft broadcast_tx_sync: %w", err)
		}
		result := chain.BroadcastResult{TxHash: resp.Hash.String(), Code: resp.Code, RawLog: resp.Log}
		if err := chain.ParseBroadcastError(result); err != nil {
			return result, err
		}
		return result, nil
	}
	res, err := c.broadcast.BroadcastSync(ctx, txBytes)
	if err != nil {
		return res, err
	}
	if err := chain.ParseBroadcastError(res); err != nil {
		return res, err
	}
	return res, nil
}

// isNotFound recognises the gRPC status the registry returns for an unknown
// agent id. Matched on the code rather than the message: NotFound is the one
// outcome that means "nothing registered under this id yet", and treating a
// transport failure as that would silently re-register an agent that already
// exists.
func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

type rpcQueryError struct {
	path      string
	code      uint32
	log       string
	codespace string
}

func (e *rpcQueryError) Error() string {
	return fmt.Sprintf("abci query %s: code %d (%s): %s", e.path, e.code, e.codespace, e.log)
}

func (c *Client) rpcQuery(ctx context.Context, path string, req, resp proto.Message) error {
	data, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", path, err)
	}
	result, err := c.rpc.ABCIQuery(ctx, path, cmtbytes.HexBytes(data))
	if err != nil {
		return fmt.Errorf("abci query %s: %w", path, err)
	}
	if result.Response.Code != 0 {
		return &rpcQueryError{path: path, code: result.Response.Code, log: result.Response.Log, codespace: result.Response.Codespace}
	}
	if err := proto.Unmarshal(result.Response.Value, resp); err != nil {
		return fmt.Errorf("unmarshal %s response: %w", path, err)
	}
	return nil
}

func isRPCNotFound(err error) bool {
	var queryErr *rpcQueryError
	return errors.As(err, &queryErr) && strings.Contains(strings.ToLower(queryErr.log), "not found")
}
