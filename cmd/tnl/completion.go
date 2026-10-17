package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

// `tnl completion bash|zsh|fish` prints a script built from helpTable. The
// scripts complete commands, subcommands and flags from tables they carry, and
// ask `tnl __complete outposts|routes|zones` (hidden) for live names.
func init() {
	commands["completion"] = func(args []string) error {
		if len(args) != 1 {
			return usageErr("completion", "usage: tnl completion bash|zsh|fish")
		}
		var script string
		switch args[0] {
		case "bash":
			script = completionBash()
		case "zsh":
			script = completionZsh()
		case "fish":
			script = completionFish()
		default:
			return usageErr("completion", "unknown shell %q; usage: tnl completion bash|zsh|fish", args[0])
		}
		_, err := fmt.Fprint(stdout, script)
		return err
	}
	commands["__complete"] = completeValues
}

// completeValues prints the names of one kind, one a line, read from the
// running process. Anything wrong prints nothing: a completion never errors.
func completeValues(args []string) error {
	if len(args) != 1 {
		return nil
	}
	dir, err := discoverStateDir(os.Getenv("TNL_STATE_DIR"))
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	v, _ := versionInfo()
	c := &api.Client{StateDir: dir, Version: v}
	var names []string
	switch args[0] {
	case "outposts":
		names = fetchNames(ctx, c, api.PathOutposts, func(l api.OutpostList) (out []string) {
			for _, o := range l.Outposts {
				out = append(out, o.Name)
			}
			return out
		})
	case "routes":
		names = fetchNames(ctx, c, api.PathRoutes, func(l api.RouteList) (out []string) {
			for _, r := range l.Routes {
				out = append(out, r.Hostname)
			}
			return out
		})
	case "zones":
		names = fetchNames(ctx, c, api.PathZones, func(l api.ZoneList) (out []string) {
			for _, z := range l.Zones {
				out = append(out, z.Name)
			}
			return out
		})
	}
	sort.Strings(names)
	for _, n := range names {
		if n != "" {
			fmt.Fprintln(stdout, n)
		}
	}
	return nil
}

// fetchNames reads a list reply leniently and picks the names out of it; a
// failed read gives none.
func fetchNames[T any](ctx context.Context, c *api.Client, path string, pick func(T) []string) []string {
	l, _, err := api.GetLenient[T](ctx, c, path)
	if err != nil {
		return nil
	}
	return pick(l)
}

// cmpNode is what a completion script knows about one command path.
type cmpNode struct {
	path     string
	subs     []string
	flags    []string          // every flag, as --name
	valFlags []string          // the flags that take a value
	flagVals map[string]string // flag -> kind of its values
	first    string            // kind of the first positional
}

func completionNodes() []cmpNode {
	top := cmpNode{subs: []string{}}
	nodes := []cmpNode{top}
	for _, c := range helpTable {
		n := cmpNode{path: c.path, flagVals: map[string]string{}, first: c.firstArg, flags: []string{"--help"}}
		if parent, name, ok := strings.Cut(c.path, " "); ok {
			for i := range nodes {
				if nodes[i].path == parent {
					nodes[i].subs = append(nodes[i].subs, name)
				}
			}
		} else {
			nodes[0].subs = append(nodes[0].subs, c.path)
		}
		for _, f := range c.allFlags() {
			n.flags = append(n.flags, f.dashed())
			if f.meta != "" && !strings.HasPrefix(f.meta, "=") {
				n.valFlags = append(n.valFlags, f.dashed())
			}
			if f.values != "" {
				n.flagVals[f.dashed()] = f.values
			}
		}
		nodes = append(nodes, n)
	}
	nodes[0].flags = []string{"--help"}
	return nodes
}

// completionData is the shell functions, in the syntax bash and zsh share,
// that answer what a path has under it.
func completionData() string {
	nodes := completionNodes()
	var paths []string
	var tops string
	for _, n := range nodes {
		if n.path != "" {
			paths = append(paths, n.path)
		}
	}
	tops = strings.Join(nodes[0].subs, " ")
	var b strings.Builder
	fn := func(name string, each func(n cmpNode) (pattern, body string)) {
		fmt.Fprintf(&b, "%s() {\n  case \"$1\" in\n", name)
		for _, n := range nodes {
			if p, body := each(n); body != "" {
				fmt.Fprintf(&b, "    %s) echo '%s' ;;\n", p, body)
			}
		}
		b.WriteString("  esac\n}\n")
	}
	quote := func(n cmpNode) string { return `"` + n.path + `"` }
	fmt.Fprintf(&b, "_tnl_ispath() {\n  case \" %s \" in *\" $1 \"*) return 0 ;; esac\n  return 1\n}\n", strings.Join(paths, " "))
	fn("_tnl_subs", func(n cmpNode) (string, string) { return quote(n), strings.Join(n.subs, " ") })
	fn("_tnl_flags", func(n cmpNode) (string, string) { return quote(n), strings.Join(n.flags, " ") })
	fn("_tnl_valflags", func(n cmpNode) (string, string) { return quote(n), strings.Join(n.valFlags, " ") })
	b.WriteString("_tnl_flagvals() {\n  case \"$1:$2\" in\n")
	for _, n := range nodes {
		var fl []string
		for f := range n.flagVals {
			fl = append(fl, f)
		}
		sort.Strings(fl)
		for _, f := range fl {
			fmt.Fprintf(&b, "    \"%s:%s\") _tnl_kind '%s' ;;\n", n.path, f, n.flagVals[f])
		}
	}
	b.WriteString("  esac\n}\n")
	b.WriteString("_tnl_first() {\n  case \"$1\" in\n")
	for _, n := range nodes {
		if n.first != "" {
			fmt.Fprintf(&b, "    %s) _tnl_kind '%s' ;;\n", quote(n), n.first)
		}
	}
	b.WriteString("  esac\n}\n")
	fmt.Fprintf(&b, "_tnl_kind() {\n  case \"$1\" in\n    words:*) echo \"${1#words:}\" ;;\n    commands) echo '%s' ;;\n    *) tnl __complete \"$1\" 2>/dev/null ;;\n  esac\n}\n", tops)
	return b.String()
}

const completionHeader = "# tnl completion for %s. Generated by `tnl completion %s`: do not edit.\n"

// The walk: words before the cursor build the command path (route add), skip
// the value of a flag that takes one, and count the other words as positionals.
// Then: a flag's value, else flags after a dash, else subcommands and the
// first positional's names. Nothing known leaves the shell's file completion.

func completionBash() string {
	return fmt.Sprintf(completionHeader, "bash", "bash") + completionData() + `_tnl() {
  local cur=${COMP_WORDS[COMP_CWORD]} prev="" cpath="" w skip=0 npos=0 i out=""
  if [[ $COMP_CWORD -gt 0 ]]; then prev=${COMP_WORDS[COMP_CWORD-1]}; fi
  for ((i = 1; i < COMP_CWORD; i++)); do
    w=${COMP_WORDS[i]}
    if [[ $skip == 1 ]]; then skip=0; continue; fi
    case "$w" in
      -*) case " $(_tnl_valflags "$cpath") " in *" $w "*) skip=1 ;; esac ;;
      *) if [[ $npos == 0 ]] && _tnl_ispath "${cpath:+$cpath }$w"; then cpath="${cpath:+$cpath }$w"; else npos=$((npos + 1)); fi ;;
    esac
  done
  if [[ $prev == -* && " $(_tnl_valflags "$cpath") " == *" $prev "* ]]; then
    out=$(_tnl_flagvals "$cpath" "$prev")
  elif [[ $cur == -* ]]; then
    out=$(_tnl_flags "$cpath")
  elif [[ $npos == 0 ]]; then
    out="$(_tnl_subs "$cpath") $(_tnl_first "$cpath")"
  fi
  COMPREPLY=($(compgen -W "$out" -- "$cur"))
}
complete -o default -F _tnl tnl
`
}

func completionZsh() string {
	return "#compdef tnl\n" + fmt.Sprintf(completionHeader, "zsh", "zsh") + completionData() + `_tnl() {
  local cur=${words[CURRENT]} prev="" cpath="" w skip=0 npos=0 i out=""
  if (( CURRENT > 1 )); then prev=${words[CURRENT-1]}; fi
  for ((i = 2; i < CURRENT; i++)); do
    w=${words[i]}
    if [[ $skip == 1 ]]; then skip=0; continue; fi
    case "$w" in
      -*) case " $(_tnl_valflags "$cpath") " in *" $w "*) skip=1 ;; esac ;;
      *) if [[ $npos == 0 ]] && _tnl_ispath "${cpath:+$cpath }$w"; then cpath="${cpath:+$cpath }$w"; else npos=$((npos + 1)); fi ;;
    esac
  done
  if [[ $prev == -* && " $(_tnl_valflags "$cpath") " == *" $prev "* ]]; then
    out=$(_tnl_flagvals "$cpath" "$prev")
  elif [[ $cur == -* ]]; then
    out=$(_tnl_flags "$cpath")
  elif [[ $npos == 0 ]]; then
    out="$(_tnl_subs "$cpath") $(_tnl_first "$cpath")"
  fi
  local -a cands
  cands=(${=out})
  if (( ${#cands} )); then compadd -- $cands; else _files; fi
}
if (( $+functions[compdef] )); then compdef _tnl tnl; fi
`
}

// completionFish is written out of the same nodes, in fish's syntax. Fish
// filters the candidates by the word being typed itself.
func completionFish() string {
	nodes := completionNodes()
	var b strings.Builder
	fmt.Fprintf(&b, completionHeader, "fish", "fish")
	var paths []string
	for _, n := range nodes {
		if n.path != "" {
			paths = append(paths, n.path)
		}
	}
	list := func(name string, each func(n cmpNode) []string) {
		fmt.Fprintf(&b, "function %s\n  switch \"$argv[1]\"\n", name)
		for _, n := range nodes {
			if items := each(n); len(items) > 0 {
				fmt.Fprintf(&b, "    case '%s'\n      printf '%%s\\n'", n.path)
				for _, it := range items {
					fmt.Fprintf(&b, " '%s'", it)
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("  end\nend\n")
	}
	fmt.Fprintf(&b, "function __tnl_paths\n  printf '%%s\\n'")
	for _, p := range paths {
		fmt.Fprintf(&b, " '%s'", p)
	}
	b.WriteString("\nend\n")
	list("__tnl_subs", func(n cmpNode) []string { return n.subs })
	list("__tnl_flags", func(n cmpNode) []string { return n.flags })
	list("__tnl_valflags", func(n cmpNode) []string { return n.valFlags })
	b.WriteString("function __tnl_flagvals\n  switch \"$argv[1]:$argv[2]\"\n")
	for _, n := range nodes {
		var fl []string
		for f := range n.flagVals {
			fl = append(fl, f)
		}
		sort.Strings(fl)
		for _, f := range fl {
			fmt.Fprintf(&b, "    case '%s:%s'\n      __tnl_kind '%s'\n", n.path, f, n.flagVals[f])
		}
	}
	b.WriteString("  end\nend\n")
	b.WriteString("function __tnl_first\n  switch \"$argv[1]\"\n")
	for _, n := range nodes {
		if n.first != "" {
			fmt.Fprintf(&b, "    case '%s'\n      __tnl_kind '%s'\n", n.path, n.first)
		}
	}
	b.WriteString("  end\nend\n")
	fmt.Fprintf(&b, `function __tnl_kind
  switch "$argv[1]"
    case 'words:*'
      string split ' ' (string sub -s 7 -- "$argv[1]")
    case commands
      printf '%%s\n' %s
    case '*'
      tnl __complete "$argv[1]" 2>/dev/null
  end
end
`, "'"+strings.Join(nodes[0].subs, "' '")+"'")
	b.WriteString(`function __tnl_complete
  set -l tokens (commandline -opc)
  set -l cur (commandline -ct)
  set -l prev $tokens[-1]
  set -e tokens[1]
  set -l cpath ""
  set -l skip 0
  set -l npos 0
  for w in $tokens
    if test $skip = 1
      set skip 0
      continue
    end
    if string match -q -- '-*' $w
      if contains -- $w (__tnl_valflags "$cpath")
        set skip 1
      end
    else if test $npos = 0; and contains -- (string trim "$cpath $w") (__tnl_paths)
      set cpath (string trim "$cpath $w")
    else
      set npos (math $npos + 1)
    end
  end
  if string match -q -- '-*' $prev; and contains -- $prev (__tnl_valflags "$cpath")
    __tnl_flagvals "$cpath" $prev
  else if string match -q -- '-*' "$cur"
    __tnl_flags "$cpath"
  else if test $npos = 0
    __tnl_subs "$cpath"
    __tnl_first "$cpath"
  end
end
complete -c tnl -f -a '(__tnl_complete)'
`)
	return b.String()
}
