package facade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oai-prism/oaiprism/internal/account"
	"github.com/oai-prism/oaiprism/internal/gateway"
	"github.com/oai-prism/oaiprism/internal/prism"
)

type gatewayStageError struct {
	stage string
	err   error
}

func (e *gatewayStageError) Error() string { return "gateway upstream stage failed: " + e.stage }
func (e *gatewayStageError) Unwrap() error { return e.err }

// Even a stateless request on an account invalidates its prior workspace epoch.
// Thus a saved sandbox is never resumed after another tenant used that account,
// regardless of whether the upstream allocates genuinely separate containers.
func (r *Runner) fenceGatewayAccount(acct *account.Account, req *RunRequest) error {
	if !req.Isolated {
		return nil
	}
	value, _ := r.accountEpoch.LoadOrStore(acct.ID, &atomic.Uint64{})
	epoch := value.(*atomic.Uint64)
	if req.GatewayResume != nil {
		state := req.GatewayResume
		if state.AccountID != acct.ID || state.Epoch == 0 || epoch.Load() != state.Epoch {
			return &gateway.APIError{Status: 409, Code: "upstream_cursor_stale", Stage: "continuation", Message: "This account has advanced beyond the saved workspace. Retry with full history; the stale request was not executed."}
		}
		req.ProjectID = state.ProjectID
		req.ConversationID = state.ConversationID
		req.PreviousResponseID = state.ResponseID
	}
	req.GatewayEpoch = epoch.Add(1)
	return nil
}
func prepareGatewaySnapshot(ctx context.Context, client *prism.Client, p prism.Principal, req *RunRequest, project string, sb *prism.Sandbox, meta map[string]any) error {
	if !req.GatewayStateful {
		return nil
	}
	if !sb.Usable() {
		return errors.New("stored conversations require a valid isolated sandbox")
	}
	var snapshot map[string]any
	if req.GatewayResume != nil {
		if json.Unmarshal(req.GatewayResume.ListenSnapshot, &snapshot) != nil || snapshot == nil {
			return errors.New("invalid stored listen snapshot")
		}
		if snapshot["conversation_id"] != req.ConversationID || snapshot["project_id"] != project {
			return errors.New("stored conversation/project binding mismatch")
		}
		if session, ok := snapshot["codex_session_id"].(string); !ok || session == "" {
			return errors.New("stored snapshot has no session")
		}
	} else {
		cid, err := client.CreateStoredConversation(ctx, p, project, req.GatewayActionID)
		if err != nil {
			return err
		}
		req.ConversationID = cid
		now := time.Now().UTC().Format(time.RFC3339Nano)
		workspace := cid
		if _, tail, ok := strings.Cut(cid, "_"); ok {
			workspace = tail
		}
		snapshot = map[string]any{"user_id": req.UserID, "project_id": project, "conversation_id": cid, "workspace_session_id": workspace, "codex_session_id": nil, "last_turn_id": nil, "endpoint_identity": nil, "last_exec_at": nil, "transcript_cursor": 0, "created_at": now, "updated_at": now, "last_saved_at": nil}
	}
	// Only credentials already owned by the selected account can enter private metadata.
	snapshot["sandbox_url"] = sb.URL
	snapshot["sandbox_token"] = sb.Token
	snapshot["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("invalid listen snapshot size")
	}
	meta["codex_listen_snapshot"] = string(raw)
	return nil
}
func captureGatewayState(req *RunRequest, result *RunResult, sb *prism.Sandbox) *gateway.UpstreamState {
	if !req.GatewayStateful || result == nil || result.ResponseID == "" || !sb.Usable() || len(result.ListenSnapshot) > 64<<10 {
		return nil
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(result.ListenSnapshot, &snapshot) != nil || snapshot == nil {
		return nil
	}
	var cid, pid, session string
	if json.Unmarshal(snapshot["conversation_id"], &cid) != nil || json.Unmarshal(snapshot["project_id"], &pid) != nil || json.Unmarshal(snapshot["codex_session_id"], &session) != nil {
		return nil
	}
	var cursor int64
	if json.Unmarshal(snapshot["transcript_cursor"], &cursor) != nil || cursor < 0 || session == "" || len(session) > 256 || cid != req.ConversationID || pid != result.ProjectID {
		return nil
	}
	if result.ConversationID != "" && result.ConversationID != cid {
		return nil
	}
	delete(snapshot, "sandbox_token")
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil
	}
	sandbox, err := json.Marshal(sb)
	if err != nil || len(sandbox) > 16<<10 {
		return nil
	}
	return &gateway.UpstreamState{Epoch: req.GatewayEpoch, AccountID: result.AccountID, ProjectID: pid, ConversationID: cid, ResponseID: result.ResponseID, ListenSnapshot: raw, Sandbox: sandbox}
}
func restoreGatewaySandbox(ctx context.Context, client *prism.Client, p prism.Principal, state *gateway.UpstreamState) (*prism.Sandbox, error) {
	var sb prism.Sandbox
	if state == nil || len(state.Sandbox) > 16<<10 || json.Unmarshal(state.Sandbox, &sb) != nil || !sb.Usable() {
		return nil, errors.New("invalid private sandbox cursor")
	}
	if err := client.ProbeStoredSandbox(ctx, p, &sb); err != nil {
		return nil, fmt.Errorf("stored workspace health: %w", err)
	}
	return &sb, nil
}
