package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangxusc/block-play-table/manager/migrations"
	"github.com/tangxusc/block-play-table/pkg/store"
)

func TestManagerEnvironmentHelpers(t *testing.T) {
	t.Setenv("BPT_TEST_VALUE", "")
	if got := getenv("BPT_TEST_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("空环境变量=%q，期望 fallback", got)
	}
	t.Setenv("BPT_TEST_VALUE", "configured")
	if got := getenv("BPT_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("环境变量=%q，期望 configured", got)
	}

	fallback := 90 * time.Second
	for _, value := range []string{"", "invalid", "0s", "-1s"} {
		t.Setenv("BPT_TEST_DURATION", value)
		if got := getenvDuration("BPT_TEST_DURATION", fallback); got != fallback {
			t.Fatalf("duration %q=%s，期望 fallback %s", value, got, fallback)
		}
	}
	t.Setenv("BPT_TEST_DURATION", "45s")
	if got := getenvDuration("BPT_TEST_DURATION", fallback); got != 45*time.Second {
		t.Fatalf("合法 duration=%s", got)
	}
}

func TestManagerStoreSelectionAndMigrationConversion(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DB_DRIVER", store.SQLDriverMemory)
	memory, closeMemory, err := openStore(ctx)
	if err != nil || memory == nil || closeMemory == nil {
		t.Fatalf("打开 memory store: store=%T close=%v err=%v", memory, closeMemory != nil, err)
	}
	closeMemory()

	for _, driver := range []string{store.SQLDriverSQLite, "sqlite3"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("DB_DRIVER", driver)
			t.Setenv("DB_DSN", filepath.Join(t.TempDir(), "manager.db"))
			st, closeStore, err := openStore(ctx)
			if err != nil || st == nil || closeStore == nil {
				t.Fatalf("打开 sqlite store: store=%T close=%v err=%v", st, closeStore != nil, err)
			}
			closeStore()
		})
	}
	t.Setenv("DB_DRIVER", "unsupported")
	if _, _, err := openStore(ctx); err == nil {
		t.Fatal("未知数据库 driver 未被拒绝")
	}
	t.Setenv("DB_DRIVER", store.SQLDriverPostgres)
	t.Setenv("DB_DSN", "")
	if _, _, err := openStore(ctx); err == nil {
		t.Fatal("Postgres 缺少 DSN 未被拒绝")
	}

	converted := toStoreMigrations(migrations.All[:2])
	if len(converted) != 2 || converted[0].Version != migrations.All[0].Version || converted[1].SQL != migrations.All[1].SQL {
		t.Fatalf("migration 转换错误: %+v", converted)
	}
}

func TestEmployeeProviderOptionFollowsConfiguredURL(t *testing.T) {
	t.Setenv("EMPLOYEE_SERVICE_URL", " ")
	if options := employeeProviderOption(); len(options) != 0 {
		t.Fatalf("空 URL 返回了 %d 个 option", len(options))
	}
	t.Setenv("EMPLOYEE_SERVICE_URL", " http://employees.test ")
	if options := employeeProviderOption(); len(options) != 1 {
		t.Fatalf("配置 URL 返回了 %d 个 option", len(options))
	}
}

func TestManagerStoreErrorAndPostgresAliasBranches(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	t.Setenv("DB_DRIVER", store.SQLDriverSQLite)
	t.Setenv("DB_DSN", filepath.Join(t.TempDir(), "canceled.db"))
	if _, _, err := openStore(canceled); err == nil {
		t.Fatal("已取消 context 未中止 SQLite 初始化")
	}

	for _, driver := range []string{"postgresql", "pgx"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("DB_DRIVER", driver)
			t.Setenv("DB_DSN", "invalid://dsn")
			if _, _, err := openStore(context.Background()); err == nil {
				t.Fatalf("%s 非法 DSN 未失败", driver)
			}
		})
	}
}
