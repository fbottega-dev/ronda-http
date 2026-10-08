package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fbottega-dev/ronda-http/internal/config"
)

// listTargets uses structural validation only. It never resolves environment
// variables or accesses services, and displays only public target metadata.
func listTargets(args []string, out, errOut io.Writer) int {
	set := flags("list", errOut)
	path := set.String("config", "ronda.json", "arquivo de configuração JSON")
	if ok, code := parsed(set, args, errOut); !ok {
		return code
	}
	cfg, err := readConfiguration(*path)
	if err != nil {
		return fail(errOut, err.Error())
	}
	var text strings.Builder
	fmt.Fprintln(&text, "DESTINOS")
	for _, target := range cfg.Targets {
		fmt.Fprintf(&text, "%s  %s", target.Method, target.Name)
		if len(target.Groups) > 0 {
			fmt.Fprintf(&text, "  [grupos: %s]", strings.Join(target.Groups, ", "))
		}
		fmt.Fprintln(&text)
	}
	return printResult(out, errOut, text.String())
}

// selectTargets filters an already validated configuration. Matches are exact
// and case-sensitive against normalized names/groups, preserving source order.
// The returned slices are independent, even when no filter is provided.
func selectTargets(cfg config.Config, only, group string) (config.Config, error) {
	if only != "" && group != "" {
		return config.Config{}, errors.New("only e group não podem ser usados juntos")
	}
	selected := config.Config{Version: cfg.Version, Targets: make([]config.Target, 0, len(cfg.Targets))}
	for _, target := range cfg.Targets {
		if only != "" && target.Name != only {
			continue
		}
		if group != "" {
			found := false
			for _, candidate := range target.Groups {
				if candidate == group {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		target.ExpectStatus = append([]int(nil), target.ExpectStatus...)
		target.Groups = append([]string(nil), target.Groups...)
		selected.Targets = append(selected.Targets, target)
	}
	if len(selected.Targets) == 0 && only != "" {
		return config.Config{}, errors.New("nenhum destino corresponde a --only; use o nome exato da configuração")
	}
	if len(selected.Targets) == 0 && group != "" {
		return config.Config{}, errors.New("nenhum destino corresponde a --group; use o grupo exato da configuração")
	}
	return selected, nil
}
