package fetcher

import (
	"context"
	"errors"
	"sort"
	"strings"

	"opf/internal/core"
)

// resolveCandidates lists the dependency closure of the roots and asks the
// distro resolver for the candidate repository versions without downloading.
func resolveCandidates(ctx context.Context, resolver core.DependencyResolver, roots []string, includeOptional bool) ([]core.CandidatePackage, error) {
	if resolver == nil {
		return nil, errors.New("resolver não configurado")
	}
	rootSet := make(map[string]struct{}, len(roots))
	names := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		rootSet[core.PackageBaseName(root)] = struct{}{}
		names[root] = struct{}{}
		deps, err := resolver.ListDependencies(ctx, root, includeOptional)
		if err != nil {
			return nil, err
		}
		for _, dep := range deps {
			if dep != "" {
				names[dep] = struct{}{}
			}
		}
	}
	if len(names) == 0 {
		return nil, errors.New("nenhum pacote para resolver versões")
	}
	candidateResolver, ok := resolver.(core.CandidateResolver)
	if !ok {
		return nil, errors.New("resolver não suporta versões candidatas")
	}
	pkgs := make([]string, 0, len(names))
	for name := range names {
		pkgs = append(pkgs, name)
	}
	sort.Strings(pkgs)
	candidates, err := candidateResolver.ListCandidates(ctx, pkgs, includeOptional)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if _, isRoot := rootSet[core.PackageBaseName(candidates[i].Name)]; isRoot {
			candidates[i].IsDependency = false
		} else {
			candidates[i].IsDependency = true
		}
	}
	return candidates, nil
}
