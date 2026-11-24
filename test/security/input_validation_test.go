package security

import (
	"context"
	"strings"
	"testing"

	"opf/internal/app"
	"opf/internal/core"
	"opf/internal/inspector"
	"opf/internal/observability"
	"opf/internal/resolver"
)

// injectedPackageNames are hostile package names that must be rejected by the
// resolvers before any command reaches the isolated environment.
var injectedPackageNames = []string{
	"vim;id",
	"vim;id;",
	"$(id)",
	"`id`",
	"vim id",
	"vim\nid",
	"vim\rid",
	"vim\tid",
	"../etc/passwd",
	"../../tmp/x",
	"vim&&id",
	"vim|id",
	"vim${IFS}id",
	"vim\x00id",
	"--help",
	"-v",
	".",
	"..",
	"",
}

type resolverFactory func(exec core.CommandExecutor) core.DependencyResolver

func resolverFactories() map[string]resolverFactory {
	return map[string]resolverFactory{
		"apt": func(exec core.CommandExecutor) core.DependencyResolver {
			return resolver.NewAptResolver(exec, "amd64", "/tmp/opf-sec-1")
		},
		"rpm": func(exec core.CommandExecutor) core.DependencyResolver {
			return resolver.NewYumDnfResolver(exec, "x86_64", "/tmp/opf-sec-1")
		},
		"apk": func(exec core.CommandExecutor) core.DependencyResolver {
			return resolver.NewApkResolver(exec, "x86_64", "/tmp/opf-sec-1")
		},
	}
}

// TestResolversRejectInjectedPackageNames asserts validation happens before the
// executor is touched: zero commands for every hostile name.
func TestResolversRejectInjectedPackageNames(t *testing.T) {
	for family, factory := range resolverFactories() {
		family, factory := family, factory
		t.Run(family, func(t *testing.T) {
			for _, name := range injectedPackageNames {
				exec := &countingExecutor{outputs: []string{"yum\n"}}
				res := factory(exec)
				if _, err := res.ListDependencies(context.Background(), name, false); err == nil {
					t.Errorf("package name %q accepted by %s resolver", name, family)
				}
				if len(exec.calls) != 0 {
					t.Fatalf("executor called %d times for rejected name %q: %v", len(exec.calls), name, exec.calls)
				}
			}
		})
	}
}

// TestResolversRejectInjectedArchitectureAndWorkDir asserts the isolated
// environment parameters are validated before any command runs.
func TestResolversRejectInjectedArchitectureAndWorkDir(t *testing.T) {
	tests := []struct {
		family  string
		arch    string
		workDir string
	}{
		{"apt", "amd64;id", "/tmp/opf"},
		{"apt", "amd64`id`", "/tmp/opf"},
		{"apt", "amd64", "relative/dir"},
		{"apt", "amd64", ""},
		{"rpm", "x86_64;id", "/tmp/opf"},
		{"rpm", "x86_64", "relative"},
		{"apk", "x86_64;id", "/tmp/opf"},
		{"apk", "x86_64", "relative"},
	}
	for _, tt := range tests {
		// A counting executor proves validation ordering instead of the nil
		// guard: the executor must never be called for invalid parameters.
		exec := &countingExecutor{outputs: []string{"yum\n"}}
		var res core.DependencyResolver
		switch tt.family {
		case "apt":
			res = resolver.NewAptResolver(exec, tt.arch, tt.workDir)
		case "rpm":
			res = resolver.NewYumDnfResolver(exec, tt.arch, tt.workDir)
		default:
			res = resolver.NewApkResolver(exec, tt.arch, tt.workDir)
		}
		if _, err := res.ListDependencies(context.Background(), "vim", false); err == nil {
			t.Errorf("%s: accepted arch=%q workDir=%q", tt.family, tt.arch, tt.workDir)
		}
		if len(exec.calls) != 0 {
			t.Errorf("%s: executor called for rejected arch=%q workDir=%q", tt.family, tt.arch, tt.workDir)
		}
	}
}

// TestResolversAcceptValidNames is the positive control for the validation
// tests: legitimate names still reach the executor and parse cleanly.
func TestResolversAcceptValidNames(t *testing.T) {
	t.Run("apt", func(t *testing.T) {
		exec := &countingExecutor{outputs: []string{"", "", "vim\nlibc6\n"}}
		res := resolver.NewAptResolver(exec, "amd64", "/tmp/opf-sec-1")
		deps, err := res.ListDependencies(context.Background(), "vim", false)
		if err != nil {
			t.Fatalf("ListDependencies: %v", err)
		}
		if len(exec.calls) != 3 {
			t.Fatalf("calls = %d", len(exec.calls))
		}
		if !containsString(deps, "vim") || !containsString(deps, "libc6") {
			t.Fatalf("deps = %v", deps)
		}
	})
	t.Run("rpm", func(t *testing.T) {
		exec := &countingExecutor{outputs: []string{"yum\n", "vim\nlibc6\n"}}
		res := resolver.NewYumDnfResolver(exec, "x86_64", "/tmp/opf-sec-1")
		deps, err := res.ListDependencies(context.Background(), "vim", false)
		if err != nil {
			t.Fatalf("ListDependencies: %v", err)
		}
		if !containsString(deps, "libc6") {
			t.Fatalf("deps = %v", deps)
		}
	})
	t.Run("apk", func(t *testing.T) {
		exec := &countingExecutor{outputs: []string{"", "", "Downloading vim-9.0.1-r0.apk\nDownloading libc6-1.2-r3.apk\n"}}
		res := resolver.NewApkResolver(exec, "x86_64", "/tmp/opf-sec-1")
		deps, err := res.ListDependencies(context.Background(), "vim", false)
		if err != nil {
			t.Fatalf("ListDependencies: %v", err)
		}
		if !containsString(deps, "vim") || !containsString(deps, "libc6") {
			t.Fatalf("deps = %v", deps)
		}
	})
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// TestRepositoryURLValidation covers the additional-repository trust boundary:
// embedded credentials, non-HTTP schemes, control characters, fragments and
// conflicting TLS/GPG opt-ins must all be rejected.
func TestRepositoryURLValidation(t *testing.T) {
	type testCase struct {
		name    string
		family  string
		repos   []core.PackageRepository
		wantErr string
	}
	aptRepo := func(mutate func(*core.PackageRepository)) []core.PackageRepository {
		repo := core.PackageRepository{Name: "Interno", URL: "https://repo.example/apt", KeyFile: "/tmp/key.asc", Suite: "bookworm"}
		mutate(&repo)
		return []core.PackageRepository{repo}
	}
	rpmRepo := func(mutate func(*core.PackageRepository)) []core.PackageRepository {
		repo := core.PackageRepository{Name: "Interno", URL: "https://repo.example/rpm", KeyFile: "/tmp/key.asc"}
		mutate(&repo)
		return []core.PackageRepository{repo}
	}

	tests := []testCase{
		{"valid apt", "apt", aptRepo(func(*core.PackageRepository) {}), ""},
		{"valid rpm", "rpm", rpmRepo(func(*core.PackageRepository) {}), ""},
		{"userinfo", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "https://user:pass@repo.example/apt" }), "usuário/senha"},
		{"file scheme", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "file:///etc" }), "http ou https"},
		{"newline", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "https://repo.example/a\nb" }), "caracteres inválidos"},
		{"tab", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "https://repo.example/a\tb" }), "caracteres inválidos"},
		{"fragment", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "https://repo.example/a#frag" }), "fragmento"},
		{"missing host", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "https:///path" }), "sem host"},
		{"empty url", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "" }), "obrigatória"},
		{"http with ca", "apt", aptRepo(func(r *core.PackageRepository) {
			r.URL = "http://repo.example/apt"
			r.CAFile = "/tmp/ca.pem"
		}), "HTTP não usa TLS"},
		{"key and insecure gpg", "apt", aptRepo(func(r *core.PackageRepository) { r.InsecureSkipGPGCheck = true }), "não ambos"},
		{"key file and url", "apt", aptRepo(func(r *core.PackageRepository) { r.KeyURL = "https://repo.example/key" }), "não ambas"},
		{"missing key", "apt", aptRepo(func(r *core.PackageRepository) { r.KeyFile = "" }), "informe a chave"},
		{"ca and insecure tls", "apt", aptRepo(func(r *core.PackageRepository) {
			r.CAFile = "/tmp/ca.pem"
			r.InsecureSkipTLSVerify = true
		}), "não ambos"},
		{"suite injection", "apt", aptRepo(func(r *core.PackageRepository) { r.Suite = "bookworm;id" }), "suite APT inválida"},
		{"component injection", "apt", aptRepo(func(r *core.PackageRepository) { r.Components = []string{"main;id"} }), "component APT inválido"},
		{"name injection", "apt", aptRepo(func(r *core.PackageRepository) { r.Name = "bad\nname" }), "nome inválido"},
		{"basearch on apt", "apt", aptRepo(func(r *core.PackageRepository) { r.URL = "https://repo.example/$basearch" }), "só são suportadas em repositórios RPM"},
		{"rpm with suite", "rpm", rpmRepo(func(r *core.PackageRepository) { r.Suite = "main" }), "apenas a APT"},
		{"apk insecure tls", "apk", []core.PackageRepository{{
			Name: "Interno", URL: "https://repo.example/apk", KeyFile: "/tmp/key.pub", InsecureSkipTLSVerify: true,
		}}, "APK não suporta ignorar TLS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.NormalizePackageRepositories(tt.repos, tt.family)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("valid repository rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestDuplicateRepositorySlugRejected asserts two names that collapse to the
// same filesystem prefix are rejected before any file is written.
func TestDuplicateRepositorySlugRejected(t *testing.T) {
	_, err := core.NormalizePackageRepositories([]core.PackageRepository{
		{Name: "Repo Interno", URL: "https://repo.example/a", KeyFile: "/tmp/k", Suite: "bookworm"},
		{Name: "repo-interno", URL: "https://repo.example/b", KeyFile: "/tmp/k", Suite: "bookworm"},
	}, "apt")
	if err == nil || !strings.Contains(err.Error(), "duplicado") {
		t.Fatalf("error = %v, want duplicate slug rejection", err)
	}
}

// TestTargetArchValidation asserts a hostile uname value detected on the remote
// host cannot select an arbitrary resolver architecture.
func TestTargetArchValidation(t *testing.T) {
	osRelease := "ID=ubuntu\nVERSION_ID=\"22.04\"\n"
	for _, arch := range []string{"x86_64;id", "$(id)", "`id`", "../x86_64", "", "x86_64 && id"} {
		if _, err := app.ParseTarget(osRelease, arch); err == nil {
			t.Errorf("uname arch %q accepted", arch)
		}
	}
	if info, err := app.ParseTarget(osRelease, "x86_64\n"); err != nil {
		t.Fatalf("valid arch rejected: %v", err)
	} else if info.Arch != "amd64" {
		t.Fatalf("arch = %q", info.Arch)
	}
}

// TestInspectorRejectsInjectedPackageNames asserts the remote pre-check
// validates names before building a shell command.
func TestInspectorRejectsInjectedPackageNames(t *testing.T) {
	tm := &fakeTransferManager{}
	insp, err := inspector.New("apt", tm)
	if err != nil {
		t.Fatalf("inspector.New: %v", err)
	}
	for _, name := range []string{"vim;id", "$(id)", "`id`", "vim id", "vim\nid", "../etc/passwd"} {
		if _, err := insp.InspectPackages(context.Background(), []string{name}); err == nil {
			t.Errorf("inspector accepted %q", name)
		}
	}
	if len(tm.commands) != 0 {
		t.Fatalf("remote command issued for rejected name: %v", tm.commands)
	}
}

// TestInspectorCommandIsQuotedAndOutputIsParsed covers the positive path and
// ensures malformed inventory lines are dropped instead of trusted.
func TestInspectorCommandIsQuotedAndOutputIsParsed(t *testing.T) {
	tm := &fakeTransferManager{output: "ii\tvim\t9.0\tamd64\ngarbage line\nii\tonly-two-fields\n"}
	insp, err := inspector.New("apt", tm)
	if err != nil {
		t.Fatalf("inspector.New: %v", err)
	}
	installed, err := insp.InspectPackages(context.Background(), []string{"vim;id"})
	if err == nil {
		t.Fatalf("hostile name accepted: %+v", installed)
	}
	installed, err = insp.InspectPackages(context.Background(), []string{"vim"})
	if err != nil {
		t.Fatalf("InspectPackages: %v", err)
	}
	if len(installed) != 1 || installed[0].Name != "vim" {
		t.Fatalf("installed = %+v", installed)
	}
	if !strings.Contains(tm.commands[len(tm.commands)-1], "'vim'") {
		t.Fatalf("package name is not shell-quoted: %s", tm.commands[len(tm.commands)-1])
	}
}

// TestSyslogConfigInjection asserts tag/protocol values cannot inject control
// characters into RFC 5424 headers.
func TestSyslogConfigInjection(t *testing.T) {
	for _, tag := range []string{"opf\nfake", "opf fake", "opf\tfake", "opf;id", "opf\x00x", ""} {
		sink, err := observability.NewSyslogSink(observability.SyslogConfig{
			Enabled: true, Host: "127.0.0.1", Port: 514, Tag: tag,
		})
		if tag == "" {
			if err != nil {
				t.Fatalf("empty tag should default: %v", err)
			}
			if sink != nil {
				_ = sink.Close(context.Background())
			}
			continue
		}
		if err == nil {
			if sink != nil {
				_ = sink.Close(context.Background())
			}
			t.Errorf("tag %q accepted", tag)
		}
	}
	if _, err := observability.NewSyslogSink(observability.SyslogConfig{
		Enabled: true, Host: "127.0.0.1", Port: 514, Tag: "opf", Proto: "udp\nx",
	}); err == nil {
		t.Error("invalid syslog protocol accepted")
	}
}

// TestPinnedVersionValidation asserts resolvers accept valid pinned versions
// while rejecting injection payloads inside the version suffix before executing
// any command.
func TestPinnedVersionValidation(t *testing.T) {
	hostilePins := []string{
		"nginx=1.0;id",
		"nginx=$(id)",
		"nginx=`id`",
		"nginx=1.0|id",
		"nginx=1.0 && id",
		"nginx=1.0\nid",
		"nginx=",
		"=1.0",
	}
	for family, factory := range resolverFactories() {
		family, factory := family, factory
		t.Run(family+"/reject", func(t *testing.T) {
			for _, spec := range hostilePins {
				exec := &countingExecutor{outputs: []string{"yum\n"}}
				res := factory(exec)
				if _, err := res.ListDependencies(context.Background(), spec, false); err == nil {
					t.Errorf("%s resolver accepted hostile pinned spec %q", family, spec)
				}
				if len(exec.calls) != 0 {
					t.Fatalf("%s executor called %d times for hostile spec %q", family, len(exec.calls), spec)
				}
			}
		})
	}
	t.Run("apt/valid-pin", func(t *testing.T) {
		exec := &countingExecutor{outputs: []string{"", "", "nginx\nlibc6\n"}}
		res := resolver.NewAptResolver(exec, "amd64", "/tmp/opf-sec-1")
		if _, err := res.ListDependencies(context.Background(), "nginx=1.18.0-6ubuntu14.4", false); err != nil {
			t.Fatalf("valid APT pin rejected: %v", err)
		}
	})
	t.Run("rpm/valid-pin", func(t *testing.T) {
		exec := &countingExecutor{outputs: []string{"dnf\n", "nginx\n"}}
		res := resolver.NewYumDnfResolver(exec, "x86_64", "/tmp/opf-sec-1")
		if _, err := res.ListDependencies(context.Background(), "nginx=1.20.1-14.el9", false); err != nil {
			t.Fatalf("valid RPM pin rejected: %v", err)
		}
	})
	t.Run("apk/valid-pin", func(t *testing.T) {
		exec := &countingExecutor{outputs: []string{"", "", "Downloading curl-8.9.1-r1.apk\n"}}
		res := resolver.NewApkResolver(exec, "x86_64", "/tmp/opf-sec-1")
		if _, err := res.ListDependencies(context.Background(), "curl=8.9.1-r1", false); err != nil {
			t.Fatalf("valid APK pin rejected: %v", err)
		}
	})
}
