package core

import (
	"strings"
	"testing"
)

func TestRepositorySlug(t *testing.T) {
	tests := map[string]string{
		"Interno RPM":    "interno-rpm",
		"  repo_1 ":      "repo-1",
		"!!!":            "repo",
		"Ácido":          "cido",
		"a/b\\c":         "a-b-c",
		"nome-muito---x": "nome-muito-x",
	}
	for input, want := range tests {
		if got := RepositorySlug(input); got != want {
			t.Fatalf("RepositorySlug(%q) = %q, esperava %q", input, got, want)
		}
	}
}

func TestNormalizePackageRepositoriesAPT(t *testing.T) {
	repos, err := NormalizePackageRepositories([]PackageRepository{{
		Name:    "Interno",
		URL:     "https://repo.interno/apt",
		KeyFile: "/tmp/key.asc",
		Suite:   "bookworm",
	}}, RepositoryFamilyAPT)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if repos[0].Family != RepositoryFamilyAPT {
		t.Fatalf("família = %q", repos[0].Family)
	}
	if len(repos[0].Components) != 1 || repos[0].Components[0] != "main" {
		t.Fatalf("components = %v", repos[0].Components)
	}
}

func TestNormalizePackageRepositoriesAPTFlat(t *testing.T) {
	repos, err := NormalizePackageRepositories([]PackageRepository{{
		Name: "Flat", URL: "https://repo.interno/flat", KeyURL: "https://repo.interno/key.asc", Flat: true,
	}}, RepositoryFamilyAPT)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !repos[0].Flat || len(repos[0].Components) != 0 {
		t.Fatalf("flat = %+v", repos[0])
	}
}

func TestNormalizePackageRepositoriesRPMAndAPK(t *testing.T) {
	rpm, err := NormalizePackageRepositories([]PackageRepository{{
		Name: "RPM", URL: "https://repo.interno/el9/$basearch/", KeyFile: "/tmp/rpm.key",
	}}, RepositoryFamilyRPM)
	if err != nil {
		t.Fatalf("RPM: %v", err)
	}
	if rpm[0].Family != RepositoryFamilyRPM {
		t.Fatalf("família RPM = %q", rpm[0].Family)
	}

	apk, err := NormalizePackageRepositories([]PackageRepository{{
		Name: "APK", URL: "https://repo.interno/alpine", KeyFile: "/tmp/apk.pub",
	}}, RepositoryFamilyAPK)
	if err != nil {
		t.Fatalf("APK: %v", err)
	}
	if apk[0].Family != RepositoryFamilyAPK {
		t.Fatalf("família APK = %q", apk[0].Family)
	}
}

func TestNormalizePackageRepositoriesComponents(t *testing.T) {
	repos, err := NormalizePackageRepositories([]PackageRepository{{
		Name: "Interno", URL: "https://repo.interno/apt", KeyFile: "/tmp/key",
		Suite: "bookworm", Components: []string{" main ", "contrib", "main", ""},
	}}, RepositoryFamilyAPT)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got := strings.Join(repos[0].Components, ","); got != "main,contrib" {
		t.Fatalf("components = %q", got)
	}
}

func TestNormalizePackageRepositoriesValidation(t *testing.T) {
	validRPM := PackageRepository{
		Name: "RPM", URL: "https://repo.interno/rpm/$basearch", KeyFile: "/tmp/key",
	}
	tests := []struct {
		name    string
		repo    PackageRepository
		family  string
		wantErr string
	}{
		{"sem nome", PackageRepository{URL: "https://repo", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "nome inválido"},
		{"família inválida", PackageRepository{Name: "x", Family: "deb", URL: "https://repo", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "família inválida"},
		{"família divergente", PackageRepository{Name: "x", Family: RepositoryFamilyRPM, URL: "https://repo", KeyFile: "/k"}, RepositoryFamilyAPT, "não corresponde ao alvo"},
		{"URL vazia", PackageRepository{Name: "x", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "URL do repositório é obrigatória"},
		{"esquema inválido", PackageRepository{Name: "x", URL: "ftp://repo", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "http ou https"},
		{"sem host", PackageRepository{Name: "x", URL: "https:///path", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "sem host"},
		{"userinfo", PackageRepository{Name: "x", URL: "https://u:p@repo/path", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "usuário/senha"},
		{"http com TLS", PackageRepository{Name: "x", URL: "http://repo/path", KeyFile: "/k", Suite: "main", CAFile: "/ca"}, RepositoryFamilyAPT, "HTTP não usa TLS"},
		{"chave e URL de chave", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k", KeyURL: "https://repo/key", Suite: "main"}, RepositoryFamilyAPT, "não ambas"},
		{"sem chave", PackageRepository{Name: "x", URL: "https://repo", Suite: "main"}, RepositoryFamilyAPT, "informe a chave"},
		{"insecure com chave", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k", Suite: "main", InsecureSkipGPGCheck: true}, RepositoryFamilyAPT, "não ambos"},
		{"CA e insecure TLS", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k", Suite: "main", CAFile: "/ca", InsecureSkipTLSVerify: true}, RepositoryFamilyAPT, "não ambos"},
		{"APT flat com suite", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k", Flat: true, Suite: "bookworm"}, RepositoryFamilyAPT, "flat não aceita"},
		{"APT sem suite", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k"}, RepositoryFamilyAPT, "suite/distribuição"},
		{"RPM com suite", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k", Suite: "main"}, RepositoryFamilyRPM, "apenas a APT"},
		{"RPM com variável", validRPM, RepositoryFamilyRPM, ""},
		{"APT com variável", PackageRepository{Name: "x", URL: "https://repo/$basearch", KeyFile: "/k", Suite: "main"}, RepositoryFamilyAPT, "só são suportadas em repositórios RPM"},
		{"APK insecure TLS", PackageRepository{Name: "x", URL: "https://repo", KeyFile: "/k", InsecureSkipTLSVerify: true}, RepositoryFamilyAPK, "APK não suporta ignorar TLS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizePackageRepositories([]PackageRepository{tt.repo}, tt.family)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("erro inesperado: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("erro = %v, esperava %q", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizePackageRepositoriesDuplicateName(t *testing.T) {
	repos := []PackageRepository{
		{Name: "Interno", URL: "https://a", KeyFile: "/k", Suite: "main"},
		{Name: "interno", URL: "https://b", KeyFile: "/k", Suite: "main"},
	}
	if _, err := NormalizePackageRepositories(repos, RepositoryFamilyAPT); err == nil || !strings.Contains(err.Error(), "duplicado") {
		t.Fatalf("erro = %v", err)
	}
}

func TestNormalizePackageRepositoriesEmpty(t *testing.T) {
	repos, err := NormalizePackageRepositories(nil, RepositoryFamilyAPT)
	if err != nil || repos != nil {
		t.Fatalf("vazio = %v, %v", repos, err)
	}
}
