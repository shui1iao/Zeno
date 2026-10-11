package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

func (s *sqliteAdminDomain) CreateAdminNode(ctx context.Context, create AdminNodeCreateRequest) (AdminNode, error) {
	if err := create.normalize(); err != nil {
		return AdminNode{}, err
	}
	nodeID := normalizeAdminNodeID(create.ID)
	if nodeID == "" {
		generated, err := generatedAdminNodeID(create.DisplayName)
		if err != nil {
			return AdminNode{}, err
		}
		nodeID = generated
	}
	bootstrapCredential, err := randomAdminCredential()
	if err != nil {
		return AdminNode{}, err
	}
	now := time.Now().UTC().Unix()
	disabled := 0
	if create.Disabled {
		disabled = 1
	}
	var quota any
	if create.MonthlyQuotaBytes.Set && create.MonthlyQuotaBytes.Valid {
		quota = create.MonthlyQuotaBytes.Value
	}
	var renewalAmount any
	if create.RenewalAmount.Set && create.RenewalAmount.Valid {
		renewalAmount = create.RenewalAmount.Value
	}
	monthlyResetDay := 1
	if create.MonthlyResetDay != nil {
		monthlyResetDay = *create.MonthlyResetDay
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNode{}, err
	}
	defer func() { rollbackUnlessCommitted(tx) }()

	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO nodes (id, display_name, token_hash, install_token, status, country_code, region, expiry_date, expiry_permanent, billing_cycle, renewal_amount, renewal_currency, display_order, public_ipv4, public_ipv6, billing_mode, monthly_quota_bytes, monthly_reset_day, disabled, created_at, updated_at, last_seen_at)
		VALUES (?, ?, ?, NULL, 'no_data', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, nodeID, create.DisplayName, hashAgentToken(bootstrapCredential), nullIfEmpty(create.CountryCode), nullIfEmpty(create.Region), nullIfEmpty(create.ExpiryDate), sqliteBoolInt(create.ExpiryPermanent), nullIfEmpty(create.BillingCycle), renewalAmount, create.RenewalCurrency, create.DisplayOrder, nullIfEmpty(create.PublicIPv4), nullIfEmpty(create.PublicIPv6), create.BillingMode, quota, monthlyResetDay, disabled, now, now)
	if err != nil {
		return AdminNode{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AdminNode{}, err
	}
	if affected == 0 {
		return AdminNode{}, errNodeAlreadyExists
	}
	// An empty scope means "all servers", so those rules already cover the new
	// node. Copy only rules with explicit scopes to preserve their restricted
	// mode while making every existing notification type include the new node.
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO alert_rule_node_scopes (rule_id, node_id, created_at)
		SELECT DISTINCT existing.rule_id, ?, ?
		FROM alert_rule_node_scopes existing
	`, nodeID, now); err != nil {
		return AdminNode{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO node_probe_targets (node_id, target_id, enabled)
		SELECT ?, pt.id, 0
		FROM probe_targets pt
		WHERE NOT EXISTS (
			SELECT 1 FROM admin_deletion_jobs deletion
			WHERE deletion.entity_kind = 'probe_target'
			  AND deletion.entity_id = pt.id
			  AND deletion.state IN ('pending', 'running')
		)
	`, nodeID); err != nil {
		return AdminNode{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminNode{}, err
	}
	tx = nil
	return s.adminNodeByID(ctx, nodeID)
}

type AgentInstallCommands struct {
	Linux               string
	MacOS               string
	Windows             string
	EnrollmentExpiresAt time.Time
}

func (commands AgentInstallCommands) Map() map[string]string {
	return map[string]string{
		"linux":   commands.Linux,
		"macos":   commands.MacOS,
		"windows": commands.Windows,
	}
}

func (s *sqliteAdminDomain) AdminNodeInstallCommand(ctx context.Context, nodeID, controllerURL, agentVersion string) (AgentInstallCommands, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return AgentInstallCommands{}, errNodeNotFound
	}
	enrollmentToken, expiresAt, err := s.agentAccess.issueAgentEnrollment(ctx, nodeID)
	if err != nil {
		return AgentInstallCommands{}, err
	}
	commands := buildAgentInstallCommands(controllerURL, nodeID, enrollmentToken, agentVersion)
	commands.EnrollmentExpiresAt = expiresAt
	return commands, nil
}

func (s *sqliteAdminDomain) DeleteAdminNode(ctx context.Context, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return errNodeNotFound
	}
	return s.enqueueAdminNodeDeletion(ctx, nodeID)
}

func (s *sqliteAdminDomain) adminNodeByID(ctx context.Context, nodeID string) (AdminNode, error) {
	return adminResourceByID(ctx, nodeID, s.AdminNodes, func(node AdminNode) string { return node.ID }, errNodeNotFound)
}

func adminResourceByID[T any](ctx context.Context, requestedID string, list func(context.Context) ([]T, error), resourceID func(T) string, notFound error) (T, error) {
	resources, err := list(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	for _, resource := range resources {
		if resourceID(resource) == requestedID {
			return resource, nil
		}
	}
	var zero T
	return zero, notFound
}

func generatedAdminNodeID(displayName string) (string, error) {
	base := normalizeAdminNodeID(displayName)
	if base == "" {
		base = "node"
	}
	suffix, err := randomHex(4)
	if err != nil {
		return "", err
	}
	return base + "-" + suffix, nil
}

func normalizeAdminNodeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '_' || unicode.IsSpace(r) {
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func randomAdminCredential() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func buildAgentInstallCommands(controllerURL, nodeID, enrollmentToken, agentVersion string) AgentInstallCommands {
	controllerURL = strings.TrimRight(strings.TrimSpace(controllerURL), "/")
	if controllerURL == "" {
		controllerURL = "http://127.0.0.1:18980"
	}
	versionEnv := " ZENO_AGENT_VERSION=" + shellSingleQuote(strings.TrimSpace(agentVersion))
	windowsVersionEnv := ""
	if strings.TrimSpace(agentVersion) != "" {
		version := strings.TrimSpace(agentVersion)
		windowsVersionEnv = "$env:ZENO_AGENT_VERSION=" + powershellSingleQuote(version) + "; "
	}
	installURL := "https://zeno.siaoz.si/agent/install.sh"
	windowsInstallURL := "https://zeno.siaoz.si/agent/install.ps1"
	insecureEnv := ""
	windowsInsecureEnv := ""
	if parsed, err := url.Parse(controllerURL); err == nil && parsed.Scheme == "http" && !loopbackURLHost(parsed.Hostname()) {
		insecureEnv = " ZENO_ALLOW_INSECURE_HTTP='1'"
		windowsInsecureEnv = "$env:ZENO_ALLOW_INSECURE_HTTP='1'; "
	}
	unixRunner := `bash -o pipefail -c 'curl -fsSL "$ZENO_INSTALL_URL" | sudo env ZENO_CONTROLLER_URL="$ZENO_CONTROLLER_URL" ZENO_NODE_ID="$ZENO_NODE_ID" ZENO_ENROLLMENT_TOKEN="$ZENO_ENROLLMENT_TOKEN" ZENO_AGENT_VERSION="$ZENO_AGENT_VERSION" ZENO_ALLOW_INSECURE_HTTP="$ZENO_ALLOW_INSECURE_HTTP" bash'`
	return AgentInstallCommands{
		Linux:   fmt.Sprintf(`ZENO_INSTALL_URL=%s ZENO_CONTROLLER_URL=%s ZENO_NODE_ID=%s ZENO_ENROLLMENT_TOKEN=%s%s%s %s`, shellSingleQuote(installURL), shellSingleQuote(controllerURL), shellSingleQuote(nodeID), shellSingleQuote(enrollmentToken), versionEnv, insecureEnv, unixRunner),
		MacOS:   fmt.Sprintf(`ZENO_INSTALL_URL=%s ZENO_CONTROLLER_URL=%s ZENO_NODE_ID=%s ZENO_ENROLLMENT_TOKEN=%s%s%s %s`, shellSingleQuote(installURL), shellSingleQuote(controllerURL), shellSingleQuote(nodeID), shellSingleQuote(enrollmentToken), versionEnv, insecureEnv, unixRunner),
		Windows: fmt.Sprintf(`powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; %s%s$env:ZENO_CONTROLLER_URL=%s; $env:ZENO_NODE_ID=%s; $env:ZENO_ENROLLMENT_TOKEN=%s; irm %s | iex"`, windowsVersionEnv, windowsInsecureEnv, powershellSingleQuote(controllerURL), powershellSingleQuote(nodeID), powershellSingleQuote(enrollmentToken), powershellSingleQuote(windowsInstallURL)),
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
