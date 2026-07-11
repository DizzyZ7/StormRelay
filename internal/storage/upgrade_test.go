//go:build upgrade

package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/jackc/pgx/v5"
)

const (
	upgradeFixtureVersion = int64(6)
	upgradeTenantID       = "00000000-0000-4000-8000-000000000001"
)

func TestUpgradeFromVersion6ToCurrent(t *testing.T) {
	if ExpectedMigrationVersion != upgradeFixtureVersion+1 {
		t.Fatalf("upgrade fixture covers v%d -> v%d, but current expected version is %d; add an explicit new upgrade boundary", upgradeFixtureVersion, upgradeFixtureVersion+1, ExpectedMigrationVersion)
	}

	ctx := context.Background()
	databaseURL := os.Getenv("STORMRELAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable"
	}

	resetUpgradeDatabase(t, ctx, databaseURL)
	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i + 1)
	}
	box, err := cryptox.NewBox(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	legacy, err := Open(ctx, databaseURL, box, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyEmbeddedMigrationsThrough(ctx, legacy, upgradeFixtureVersion); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	version, err := legacy.MigrationVersion(ctx)
	if err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if version != upgradeFixtureVersion {
		legacy.Close()
		t.Fatalf("legacy migration version=%d, want %d", version, upgradeFixtureVersion)
	}

	var oidcAbsent bool
	if err := legacy.pool.QueryRow(ctx, `SELECT to_regclass('public.oidc_providers') IS NULL`).Scan(&oidcAbsent); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if !oidcAbsent {
		legacy.Close()
		t.Fatal("v6 fixture unexpectedly contains OIDC tables")
	}

	source, err := legacy.CreateSource(ctx, CreateSourceInput{
		TenantID: upgradeTenantID,
		Name:     "upgrade-fixture-source",
		Kind:     "generic",
		AuthMode: ingestion.AuthHMAC,
	})
	if err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	account, err := legacy.CreateServiceAccount(ctx, CreateServiceAccountInput{
		TenantID: upgradeTenantID,
		Name:     "upgrade-fixture-service-account",
		Roles:    []auth.Role{auth.RoleViewer},
		ActorID:  "upgrade-fixture-admin",
	})
	if err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	key, err := legacy.CreateServiceAccountKey(ctx, CreateServiceAccountKeyInput{
		TenantID:         upgradeTenantID,
		ServiceAccountID: account.ID,
		ActorID:          "upgrade-fixture-admin",
	})
	if err != nil {
		legacy.Close()
		t.Fatal(err)
	}

	var sourceCount, accountCount, auditCount int
	if err := legacy.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM event_sources WHERE id=$1::uuid),
			(SELECT count(*) FROM service_accounts WHERE id=$2::uuid),
			(SELECT count(*) FROM audit_entries WHERE resource_id=$3)
	`, source.Source.ID, account.ID, account.ID).Scan(&sourceCount, &accountCount, &auditCount); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if sourceCount != 1 || accountCount != 1 || auditCount == 0 {
		legacy.Close()
		t.Fatalf("legacy fixture counts: source=%d account=%d audit=%d", sourceCount, accountCount, auditCount)
	}
	legacy.Close()

	upgraded, err := Open(ctx, databaseURL, box, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	version, err = upgraded.MigrationVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != ExpectedMigrationVersion {
		t.Fatalf("upgraded migration version=%d, want %d", version, ExpectedMigrationVersion)
	}

	credentials, err := upgraded.GetSourceCredentials(ctx, source.Source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(credentials.HMACSecret) != source.Credential {
		t.Fatal("restored source credential does not decrypt with the preserved master key")
	}
	principal, err := upgraded.AuthenticateServiceAccountKey(ctx, key.Credential)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != upgradeTenantID || principal.ActorID != account.ID || !principal.Allowed(auth.PermissionIncidentsRead) {
		t.Fatalf("upgraded service-account principal is invalid: %+v", principal)
	}

	if err := assertOIDCMigrationConstraints(ctx, upgraded); err != nil {
		t.Fatal(err)
	}

	if err := upgraded.Migrate(ctx); err != nil {
		t.Fatalf("second migration pass was not idempotent: %v", err)
	}
	var migrationCount, migrationMin, migrationMax int64
	if err := upgraded.pool.QueryRow(ctx, `SELECT count(*), min(version), max(version) FROM schema_migrations`).Scan(&migrationCount, &migrationMin, &migrationMax); err != nil {
		t.Fatal(err)
	}
	if migrationCount != ExpectedMigrationVersion || migrationMin != 1 || migrationMax != ExpectedMigrationVersion {
		t.Fatalf("migration history count=%d min=%d max=%d", migrationCount, migrationMin, migrationMax)
	}

	if err := upgraded.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM event_sources WHERE id=$1::uuid),
			(SELECT count(*) FROM service_accounts WHERE id=$2::uuid),
			(SELECT count(*) FROM audit_entries WHERE resource_id=$3)
	`, source.Source.ID, account.ID, account.ID).Scan(&sourceCount, &accountCount, &auditCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 || accountCount != 1 || auditCount == 0 {
		t.Fatalf("post-upgrade fixture counts: source=%d account=%d audit=%d", sourceCount, accountCount, auditCount)
	}
}

func resetUpgradeDatabase(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
}

func applyEmbeddedMigrationsThrough(ctx context.Context, store *Store, maximumVersion int64) error {
	conn, err := store.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix := strings.SplitN(entry.Name(), "_", 2)[0]
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid migration %s", entry.Name())
		}
		if version > maximumVersion {
			continue
		}
		sqlDocument, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlDocument)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply legacy migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func assertOIDCMigrationConstraints(ctx context.Context, store *Store) error {
	providerID := "00000000-0000-4000-8000-000000000701"
	_, err := store.pool.Exec(ctx, `
		INSERT INTO oidc_providers(id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs)
		VALUES($1,$2,'upgrade-provider','https://issuer.upgrade.example','stormrelay-upgrade','https://issuer.upgrade.example/jwks',ARRAY['RS256']::text[])
	`, providerID, upgradeTenantID)
	if err != nil {
		return fmt.Errorf("insert valid OIDC provider: %w", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO oidc_providers(id,tenant_id,name,issuer,audience,jwks_uri)
		VALUES('00000000-0000-4000-8000-000000000702',$1,'duplicate-provider','https://issuer.upgrade.example','stormrelay-upgrade','https://issuer.upgrade.example/other-jwks')
	`, upgradeTenantID); err == nil {
		return fmt.Errorf("OIDC issuer/audience uniqueness constraint was not enforced")
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO oidc_providers(id,tenant_id,name,issuer,audience,jwks_uri)
		VALUES('00000000-0000-4000-8000-000000000703',$1,'insecure-provider','http://issuer.invalid','invalid-audience','https://issuer.invalid/jwks')
	`, upgradeTenantID); err == nil {
		return fmt.Errorf("OIDC HTTPS issuer constraint was not enforced")
	}
	var providerCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM oidc_providers WHERE id=$1::uuid`, providerID).Scan(&providerCount); err != nil {
		return err
	}
	if providerCount != 1 {
		return fmt.Errorf("valid OIDC provider count=%d", providerCount)
	}
	return nil
}
