package surf

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestNewAppDefaults(t *testing.T) {
	app := NewApp()

	if app.router == nil {
		t.Error("router should not be nil")
	}
	if app.services == nil {
		t.Error("services should not be nil")
	}
	if app.logger == nil {
		t.Error("logger should not be nil")
	}
	if app.ctx == nil {
		t.Error("ctx should not be nil")
	}
	if app.cancel == nil {
		t.Error("cancel should not be nil")
	}
	if app.shutdown == nil {
		t.Error("shutdown should not be nil")
	}

	// Check default server config
	if app.serverConfig.Addr != ":8080" {
		t.Errorf("default addr = %q, want %q", app.serverConfig.Addr, ":8080")
	}
	if app.serverConfig.ReadTimeout != 15*time.Second {
		t.Errorf("default read timeout = %v, want %v", app.serverConfig.ReadTimeout, 15*time.Second)
	}
}

func TestNewAppWithOptions(t *testing.T) {
	customLogger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	customCtx, customCancel := context.WithCancel(context.Background())
	defer customCancel()

	app := NewApp(
		WithLogger(customLogger),
		WithContext(customCtx),
		WithAddr(":3000"),
		WithReadTimeout(30*time.Second),
		WithWriteTimeout(30*time.Second),
		WithIdleTimeout(120*time.Second),
		WithMaxHeaderBytes(2<<20),
	)

	if app.logger != customLogger {
		t.Error("custom logger not set")
	}
	if app.serverConfig.Addr != ":3000" {
		t.Errorf("addr = %q, want %q", app.serverConfig.Addr, ":3000")
	}
	if app.serverConfig.ReadTimeout != 30*time.Second {
		t.Errorf("read timeout = %v, want %v", app.serverConfig.ReadTimeout, 30*time.Second)
	}
	if app.serverConfig.WriteTimeout != 30*time.Second {
		t.Errorf("write timeout = %v, want %v", app.serverConfig.WriteTimeout, 30*time.Second)
	}
	if app.serverConfig.IdleTimeout != 120*time.Second {
		t.Errorf("idle timeout = %v, want %v", app.serverConfig.IdleTimeout, 120*time.Second)
	}
	if app.serverConfig.MaxHeaderBytes != 2<<20 {
		t.Errorf("max header bytes = %d, want %d", app.serverConfig.MaxHeaderBytes, 2<<20)
	}
}

func TestAppServiceContainer(t *testing.T) {
	app := NewApp()

	type TestService struct {
		Name string
	}

	svc := &TestService{Name: "test"}
	app.Set("myService", svc)

	retrieved := app.GetService("myService")
	if retrieved == nil {
		t.Fatal("service should not be nil")
	}

	typedSvc, ok := retrieved.(*TestService)
	if !ok {
		t.Fatal("service should be *TestService")
	}
	if typedSvc.Name != "test" {
		t.Errorf("service name = %q, want %q", typedSvc.Name, "test")
	}
}

func TestAppServiceContainerConcurrent(t *testing.T) {
	app := NewApp()

	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 100; i++ {
		go func(n int) {
			app.Set(n, n*2)
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 100; i++ {
		<-done
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		go func(n int) {
			val := app.GetService(n)
			if val != n*2 {
				t.Errorf("service %d = %v, want %d", n, val, n*2)
			}
			done <- true
		}(i)
	}

	// Wait for all reads
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestAppCleanup(t *testing.T) {
	app := NewApp()

	// Cleanup should not panic
	app.Cleanup()

	// Context should be cancelled
	select {
	case <-app.ctx.Done():
		// expected
	default:
		t.Error("context should be cancelled after cleanup")
	}
}

func TestDefaultServerConfig(t *testing.T) {
	config := DefaultServerConfig()

	if config.Addr != ":8080" {
		t.Errorf("addr = %q, want %q", config.Addr, ":8080")
	}
	if config.ReadTimeout != 15*time.Second {
		t.Errorf("read timeout = %v, want %v", config.ReadTimeout, 15*time.Second)
	}
	if config.WriteTimeout != 15*time.Second {
		t.Errorf("write timeout = %v, want %v", config.WriteTimeout, 15*time.Second)
	}
	if config.IdleTimeout != 60*time.Second {
		t.Errorf("idle timeout = %v, want %v", config.IdleTimeout, 60*time.Second)
	}
	if config.MaxHeaderBytes != 1<<20 {
		t.Errorf("max header bytes = %d, want %d", config.MaxHeaderBytes, 1<<20)
	}
}

func TestWithServerConfig(t *testing.T) {
	customConfig := ServerConfig{
		Addr:           ":9000",
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    30 * time.Second,
		MaxHeaderBytes: 512 << 10,
	}

	app := NewApp(WithServerConfig(customConfig))

	if app.serverConfig != customConfig {
		t.Error("custom server config not applied")
	}
}

func TestWithShutdownChannel(t *testing.T) {
	customShutdown := make(chan os.Signal, 1)
	app := NewApp(WithShutdownChannel(customShutdown))

	if app.shutdown != customShutdown {
		t.Error("custom shutdown channel not set")
	}
}
