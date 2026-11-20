package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/client"
)

func newTestProvider(env map[string]string) *ledgerProvider {
	return &ledgerProvider{version: "test", getenv: envOf(env), readFile: os.ReadFile}
}

func schemaOf(t *testing.T, p provider.Provider) provider.SchemaResponse {
	t.Helper()
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp
}

// config builds a provider config; a nil pointer is null, and unknown marks a value unknown.
func config(t *testing.T, p provider.Provider, endpoint, token, tokenFile any) tfsdk.Config {
	t.Helper()
	val := func(v any) tftypes.Value {
		switch x := v.(type) {
		case nil:
			return tftypes.NewValue(tftypes.String, nil)
		case string:
			return tftypes.NewValue(tftypes.String, x)
		default:
			return tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
		}
	}
	sch := schemaOf(t, p).Schema
	return tfsdk.Config{
		Schema: sch,
		Raw: tftypes.NewValue(sch.Type().TerraformType(context.Background()), map[string]tftypes.Value{
			"endpoint": val(endpoint), "token": val(token), "token_file": val(tokenFile),
		}),
	}
}

func configure(t *testing.T, p provider.Provider, cfg tfsdk.Config) *provider.ConfigureResponse {
	t.Helper()
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{Config: cfg}, resp)
	return resp
}

func TestSchemaIsValidAndTokenIsSensitive(t *testing.T) {
	p := New("test")()
	resp := schemaOf(t, p)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	if !resp.Schema.Attributes["token"].IsSensitive() {
		t.Error("token must be sensitive")
	}
	if resp.Schema.Attributes["token_file"].IsSensitive() {
		t.Error("token_file is a path, not a secret")
	}
}

func TestConfigureFromEnvironment(t *testing.T) {
	p := newTestProvider(map[string]string{EnvEndpoint: "https://ledger.example/", EnvToken: "env-token"})
	resp := configure(t, p, config(t, p, nil, nil, nil))
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	c, ok := resp.ResourceData.(*client.Client)
	if !ok || resp.DataSourceData != resp.ResourceData {
		t.Fatalf("client not shared with resources and data sources: %T", resp.ResourceData)
	}
	if got := c.BaseURL().String(); got != "https://ledger.example" {
		t.Errorf("base URL = %q", got)
	}
}

func TestConfigureConfigBeatsEnvironment(t *testing.T) {
	p := newTestProvider(map[string]string{EnvEndpoint: "https://env.example", EnvToken: "env-token"})
	resp := configure(t, p, config(t, p, "https://cfg.example", "cfg-token", nil))
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	if got := resp.ResourceData.(*client.Client).BaseURL().Host; got != "cfg.example" {
		t.Errorf("host = %q, want cfg.example", got)
	}
}

func TestConfigureTokenFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newTestProvider(map[string]string{EnvEndpoint: "https://env.example"})
	resp := configure(t, p, config(t, p, nil, nil, file))
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
}

func TestConfigureErrorsDoNotLeakToken(t *testing.T) {
	const secret = "tok-secret-123"
	p := newTestProvider(nil)
	for name, cfg := range map[string]tfsdk.Config{
		"credentials in endpoint": config(t, p, "https://user:"+secret+"@ledger.example", secret, nil),
		"bad scheme":              config(t, p, "ftp://ledger.example", secret, nil),
		"unknown token":           config(t, p, "https://ledger.example", struct{}{}, nil),
		"missing endpoint":        config(t, p, nil, secret, nil),
	} {
		t.Run(name, func(t *testing.T) {
			resp := configure(t, p, cfg)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error")
			}
			for _, d := range resp.Diagnostics {
				if strings.Contains(d.Summary()+d.Detail(), secret) {
					t.Errorf("diagnostic leaks secret: %s / %s", d.Summary(), d.Detail())
				}
			}
			if resp.ResourceData != nil {
				t.Error("no client expected on error")
			}
		})
	}
}

func TestConfigureWarnsOnPlainHTTPRemote(t *testing.T) {
	p := newTestProvider(nil)
	resp := configure(t, p, config(t, p, "http://ledger.example", "tok", nil))
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("want one warning, got %v", resp.Diagnostics)
	}
	resp = configure(t, p, config(t, p, "http://127.0.0.1:8080", "tok", nil))
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("loopback http must not warn: %v", resp.Diagnostics)
	}
}

func TestNoResourcesYet(t *testing.T) {
	p := New("test")()
	if len(p.Resources(context.Background())) != 0 || len(p.DataSources(context.Background())) != 0 {
		t.Error("scaffold registers no resources; see the TODO(management-api) markers")
	}
}
