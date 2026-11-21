package core

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"opf/internal/i18n"
)

// Additional repository families. They match the package manager family used
// by the isolated download environment.
const (
	RepositoryFamilyAPT = "apt"
	RepositoryFamilyRPM = "rpm"
	RepositoryFamilyAPK = "apk"
)

var (
	repoNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
	repoSuitePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	repoSlugReplacer     = regexp.MustCompile(`[^a-z0-9]+`)
	repoControlCharsExpr = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

// PackageRepository describes an additional package repository used only
// inside the isolated download environment. It is deliberately named
// PackageRepository to avoid clashing with the persistence Repository
// interface. No authentication material is part of this version.
type PackageRepository struct {
	Name                  string   `json:"name"`
	Family                string   `json:"family"`
	URL                   string   `json:"url"`
	KeyFile               string   `json:"key_file,omitempty"`
	KeyURL                string   `json:"key_url,omitempty"`
	CAFile                string   `json:"ca_file,omitempty"`
	Suite                 string   `json:"suite,omitempty"`
	Components            []string `json:"components,omitempty"`
	Flat                  bool     `json:"flat,omitempty"`
	InsecureSkipTLSVerify bool     `json:"insecure_skip_tls_verify,omitempty"`
	InsecureSkipGPGCheck  bool     `json:"insecure_skip_gpg_check,omitempty"`
}

// RepositorySlug converts a human name into a filesystem-safe identifier used
// in repository file names inside the isolated environment.
func RepositorySlug(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = repoSlugReplacer.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "repo"
	}
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	return slug
}

// NormalizePackageRepositories trims fields, fills defaults, validates every
// entry and returns the normalized list. It performs no file I/O.
func NormalizePackageRepositories(repos []PackageRepository, targetFamily string) ([]PackageRepository, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	targetFamily = strings.ToLower(strings.TrimSpace(targetFamily))
	if !validRepositoryFamily(targetFamily) {
		return nil, fmt.Errorf(i18n.T("família de destino inválida para repositórios: %q"), targetFamily)
	}

	out := make([]PackageRepository, 0, len(repos))
	seen := make(map[string]struct{}, len(repos))
	for i, repo := range repos {
		normalized, err := normalizePackageRepository(repo, targetFamily)
		if err != nil {
			return nil, fmt.Errorf(i18n.T("repositório %d: %w"), i+1, err)
		}
		slug := RepositorySlug(normalized.Name)
		if _, duplicate := seen[slug]; duplicate {
			return nil, fmt.Errorf(i18n.T("repositório %d: nome %q duplicado (identificador %q)"), i+1, normalized.Name, slug)
		}
		seen[slug] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// ValidatePackageRepositories validates the same rules as
// NormalizePackageRepositories without returning the normalized list.
func ValidatePackageRepositories(repos []PackageRepository, targetFamily string) error {
	_, err := NormalizePackageRepositories(repos, targetFamily)
	return err
}

func normalizePackageRepository(repo PackageRepository, targetFamily string) (PackageRepository, error) {
	repo.Name = strings.TrimSpace(repo.Name)
	repo.Family = strings.ToLower(strings.TrimSpace(repo.Family))
	repo.URL = strings.TrimSpace(repo.URL)
	repo.KeyFile = strings.TrimSpace(repo.KeyFile)
	repo.KeyURL = strings.TrimSpace(repo.KeyURL)
	repo.CAFile = strings.TrimSpace(repo.CAFile)
	repo.Suite = strings.TrimSpace(repo.Suite)
	repo.Components = normalizeComponents(repo.Components)

	if repo.Family == "" {
		repo.Family = targetFamily
	}
	if !validRepositoryFamily(repo.Family) {
		return PackageRepository{}, fmt.Errorf(i18n.T("família inválida: %q"), repo.Family)
	}
	if repo.Family != targetFamily {
		return PackageRepository{}, fmt.Errorf(i18n.T("família %q não corresponde ao alvo %q"), repo.Family, targetFamily)
	}
	if !repoNamePattern.MatchString(repo.Name) {
		return PackageRepository{}, fmt.Errorf(i18n.T("nome inválido: %q (use letras, números, espaço, ponto, hífen ou sublinhado)"), repo.Name)
	}
	if err := validateRepositoryURL(repo.URL, repo.Family); err != nil {
		return PackageRepository{}, err
	}
	if repo.KeyFile != "" && repo.KeyURL != "" {
		return PackageRepository{}, errors.New(i18n.T("informe chave por arquivo ou URL, não ambas"))
	}
	if repo.InsecureSkipGPGCheck && (repo.KeyFile != "" || repo.KeyURL != "") {
		return PackageRepository{}, errors.New(i18n.T("desative a chave ou o opt-in inseguro de GPG, não ambos"))
	}
	if !repo.InsecureSkipGPGCheck && repo.KeyFile == "" && repo.KeyURL == "" {
		return PackageRepository{}, errors.New(i18n.T("informe a chave do repositório ou habilite o opt-in inseguro de GPG"))
	}
	if repo.CAFile != "" && repo.InsecureSkipTLSVerify {
		return PackageRepository{}, errors.New(i18n.T("informe CA ou opt-in inseguro de TLS, não ambos"))
	}
	if strings.HasPrefix(strings.ToLower(repo.URL), "http://") && (repo.CAFile != "" || repo.InsecureSkipTLSVerify) {
		return PackageRepository{}, errors.New(i18n.T("repositório HTTP não usa TLS; remova CA/insecure TLS"))
	}
	if repo.Family == RepositoryFamilyAPK && repo.InsecureSkipTLSVerify {
		return PackageRepository{}, errors.New(i18n.T("APK não suporta ignorar TLS por repositório; use CA customizada"))
	}

	switch repo.Family {
	case RepositoryFamilyAPT:
		if repo.Flat {
			if repo.Suite != "" || len(repo.Components) > 0 {
				return PackageRepository{}, errors.New(i18n.T("repositório flat não aceita suite nem components"))
			}
		} else {
			if repo.Suite == "" {
				return PackageRepository{}, errors.New(i18n.T("informe a suite/distribuição APT"))
			}
			if !repoSuitePattern.MatchString(repo.Suite) {
				return PackageRepository{}, fmt.Errorf(i18n.T("suite APT inválida: %q"), repo.Suite)
			}
			if len(repo.Components) == 0 {
				repo.Components = []string{"main"}
			}
			for _, component := range repo.Components {
				if !repoSuitePattern.MatchString(component) {
					return PackageRepository{}, fmt.Errorf(i18n.T("component APT inválido: %q"), component)
				}
			}
		}
	case RepositoryFamilyRPM, RepositoryFamilyAPK:
		if repo.Suite != "" || len(repo.Components) > 0 || repo.Flat {
			return PackageRepository{}, errors.New(i18n.T("suite/components/flat aplicam-se apenas a APT"))
		}
	}
	return repo, nil
}

func validateRepositoryURL(raw, family string) error {
	if raw == "" {
		return errors.New(i18n.T("URL do repositório é obrigatória"))
	}
	if repoControlCharsExpr.MatchString(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return errors.New(i18n.T("URL do repositório contém caracteres inválidos"))
	}
	if family != RepositoryFamilyRPM && strings.Contains(raw, "$") {
		return errors.New(i18n.T("variáveis como $basearch só são suportadas em repositórios RPM/DNF"))
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf(i18n.T("URL do repositório inválida: %w"), err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf(i18n.T("URL do repositório deve usar http ou https: %q"), raw)
	}
	if parsed.Host == "" {
		return fmt.Errorf(i18n.T("URL do repositório sem host: %q"), raw)
	}
	if parsed.User != nil {
		return errors.New(i18n.T("URL do repositório não pode conter usuário/senha"))
	}
	if parsed.Fragment != "" {
		return errors.New(i18n.T("URL do repositório não pode conter fragmento"))
	}
	return nil
}

func normalizeComponents(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validRepositoryFamily(family string) bool {
	switch family {
	case RepositoryFamilyAPT, RepositoryFamilyRPM, RepositoryFamilyAPK:
		return true
	default:
		return false
	}
}
