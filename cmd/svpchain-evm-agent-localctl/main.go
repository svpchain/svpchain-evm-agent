// Command svpchain-evm-agent-localctl authenticates an operator to a running
// local agent and invokes its registry lifecycle operations. The agent, not
// this command, signs and broadcasts the registry transaction.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/svpchain/svpchain-evm-agent/internal/mcp/signer"
)

const executionSkill = "svpchain-execution"

var coinPattern = regexp.MustCompile(`^([0-9]+)([a-zA-Z][a-zA-Z0-9/._:-]*)$`)

type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type identity struct {
	Operator   string `json:"operator"`
	AgentID    string `json:"agent_id"`
	Registered bool   `json:"registered"`
	Endpoint   string `json:"endpoint"`
	CardHash   string `json:"card_hash"`
}

type challenge struct {
	Challenge string `json:"challenge"`
	Nonce     string `json:"nonce"`
}

type verify struct {
	BearerToken string `json:"bearer_token"`
	Owner       string `json:"owner"`
}

type coin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

type txResult struct {
	TxHash string `json:"tx_hash"`
	Code   uint32 `json:"code"`
	RawLog string `json:"raw_log"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "svpchain-evm-agent-localctl: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	action := flag.String("action", "", "action: address, register, or update")
	agentURL := flag.String("agent-url", "", "base URL of the running agent")
	keyFile := flag.String("key-file", "", "file containing the operator's 32-byte hex private key")
	bondText := flag.String("bond", "", "initial registration bond, e.g. 5000asvp (register only)")
	flag.Parse()

	if *action != "address" && *action != "register" && *action != "update" {
		return fmt.Errorf("--action must be address, register, or update")
	}
	if *keyFile == "" {
		return fmt.Errorf("--key-file is required")
	}
	if *action != "address" && *agentURL == "" {
		return fmt.Errorf("--agent-url is required for %s", *action)
	}
	if *action == "update" && *bondText != "" {
		return fmt.Errorf("--bond is only valid with --action register")
	}

	rawKey, err := os.ReadFile(*keyFile)
	if err != nil {
		return fmt.Errorf("read operator key: %w", err)
	}
	priv, err := signer.ParsePrivKey(string(rawKey))
	if err != nil {
		return fmt.Errorf("parse operator key: %w", err)
	}
	operatorAddr := signer.DeriveAddress(priv)
	if *action == "address" {
		fmt.Println(operatorAddr)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := newClient(ctx, strings.TrimRight(*agentURL, "/"))
	if err != nil {
		return err
	}
	defer client.Destroy()

	identityReply, err := call(ctx, client, executionSkill, "agent_identity", map[string]any{}, "")
	if err != nil {
		return fmt.Errorf("query agent identity: %w", err)
	}
	var current identity
	if err := json.Unmarshal(identityReply.Result, &current); err != nil {
		return fmt.Errorf("decode agent identity: %w", err)
	}
	if current.Operator != operatorAddr {
		return fmt.Errorf("key-file operator %s does not match running agent operator %s", operatorAddr, current.Operator)
	}
	if *action == "register" && current.Registered {
		return fmt.Errorf("%s is already registered; use update instead", current.AgentID)
	}
	if *action == "update" && !current.Registered {
		return fmt.Errorf("%s is not registered; use register first", current.AgentID)
	}

	challengeReply, err := call(ctx, client, "svpchain-auth", "auth_challenge", map[string]any{"owner": operatorAddr}, "")
	if err != nil {
		return fmt.Errorf("request auth challenge: %w", err)
	}
	var authChallenge challenge
	if err := json.Unmarshal(challengeReply.Result, &authChallenge); err != nil {
		return fmt.Errorf("decode auth challenge: %w", err)
	}
	sig, err := priv.Sign([]byte(authChallenge.Challenge))
	if err != nil {
		return fmt.Errorf("sign auth challenge: %w", err)
	}
	verifyReply, err := call(ctx, client, "svpchain-auth", "auth_verify", map[string]any{
		"nonce": authChallenge.Nonce, "signature": base64.StdEncoding.EncodeToString(sig),
	}, "")
	if err != nil {
		return fmt.Errorf("verify operator authentication: %w", err)
	}
	var authVerify verify
	if err := json.Unmarshal(verifyReply.Result, &authVerify); err != nil {
		return fmt.Errorf("decode auth verification: %w", err)
	}
	if authVerify.Owner != operatorAddr || authVerify.BearerToken == "" {
		return fmt.Errorf("operator authentication returned an invalid identity")
	}

	args := map[string]any{}
	if *action == "register" && *bondText != "" {
		bond, err := parseCoin(*bondText)
		if err != nil {
			return fmt.Errorf("parse --bond: %w", err)
		}
		args["bond"] = bond
	}
	result, err := call(ctx, client, executionSkill, "agent_self_"+*action, args, authVerify.BearerToken)
	if err != nil {
		return fmt.Errorf("%s: %w", *action, err)
	}
	var tx txResult
	if err := json.Unmarshal(result.Result, &tx); err != nil {
		return fmt.Errorf("decode %s transaction result: %w", *action, err)
	}
	if tx.Code != 0 {
		return fmt.Errorf("%s transaction %s was rejected with code %d: %s", *action, tx.TxHash, tx.Code, tx.RawLog)
	}

	pretty, err := json.MarshalIndent(result.Result, "", "  ")
	if err != nil {
		return fmt.Errorf("format %s response: %w", *action, err)
	}
	fmt.Printf("%s accepted by the local chain\nagent id:  %s\nendpoint:  %s\ncard hash: %s\n%s\n", *action, current.AgentID, current.Endpoint, current.CardHash, pretty)
	return nil
}

func newClient(ctx context.Context, agentURL string) (*a2aclient.Client, error) {
	endpoint := a2a.NewAgentInterface(agentURL+"/invoke", a2a.TransportProtocolJSONRPC)
	client, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{endpoint}, a2aclient.WithJSONRPCTransport(&http.Client{Timeout: 30 * time.Second}))
	if err != nil {
		return nil, fmt.Errorf("connect to agent A2A endpoint: %w", err)
	}
	return client, nil
}

func call(ctx context.Context, client *a2aclient.Client, skill, tool string, args map[string]any, bearer string) (response, error) {
	envelope := map[string]any{"skill": skill, "tool": tool, "args": args}
	if bearer != "" {
		envelope["bearer"] = bearer
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return response{}, fmt.Errorf("encode %s: %w", tool, err)
	}
	result, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(string(body)))})
	if err != nil {
		return response{}, fmt.Errorf("send %s: %w", tool, err)
	}
	var reply response
	if err := json.Unmarshal([]byte(resultText(result)), &reply); err != nil {
		return response{}, fmt.Errorf("decode %s response: %w", tool, err)
	}
	if !reply.OK {
		if reply.Error == "" {
			reply.Error = "agent refused without an error message"
		}
		return response{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}

func resultText(result a2a.SendMessageResult) string {
	messageText := func(msg *a2a.Message) string {
		if msg == nil {
			return ""
		}
		var out strings.Builder
		for _, part := range msg.Parts {
			if part != nil {
				out.WriteString(part.Text())
			}
		}
		return out.String()
	}
	switch v := result.(type) {
	case *a2a.Message:
		return messageText(v)
	case *a2a.Task:
		if v.Status.Message != nil {
			if text := messageText(v.Status.Message); text != "" {
				return text
			}
		}
		for i := len(v.History) - 1; i >= 0; i-- {
			if text := messageText(v.History[i]); text != "" {
				return text
			}
		}
	}
	return ""
}

func parseCoin(value string) (coin, error) {
	matches := coinPattern.FindStringSubmatch(value)
	if len(matches) != 3 {
		return coin{}, fmt.Errorf("must be <integer><denom>, for example 5000asvp")
	}
	return coin{Amount: matches[1], Denom: matches[2]}, nil
}
