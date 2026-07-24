package command

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

func FuzzAnalyzePOSIX_CompletePlansRepresentStaticSyntax(f *testing.F) {
	for _, source := range []string{
		`git status`,
		`echo "git status"`,
		`echo ok && git status`,
		`git status | tee out.txt`,
		`echo "$(git status)"`,
		`sh -c 'git status'`,
		`env FOO=bar git status`,
		`command git status`,
		`printf done > result.txt < input.txt`,
	} {
		f.Add(source)
	}

	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > MaxSourceBytes {
			t.Skip()
		}
		plan, err := Analyze(context.Background(), Request{
			Mode: ModePOSIXShell, Command: source,
		})
		if err != nil {
			return
		}
		repeated, repeatErr := Analyze(context.Background(), Request{
			Mode: ModePOSIXShell, Command: source,
		})
		require.NoError(t, repeatErr)
		require.Equal(t, plan.Digest(), repeated.Digest())
		require.Equal(t, plan.Invocations, repeated.Invocations)
		require.Equal(t, plan.Redirects, repeated.Redirects)
		if !plan.Complete {
			return
		}

		parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
		file, parseErr := parser.Parse(strings.NewReader(source), "")
		require.NoError(t, parseErr)
		syntax.Walk(file, func(node syntax.Node) bool {
			switch value := node.(type) {
			case *syntax.CallExpr:
				if len(value.Args) == 0 {
					return true
				}
				_, static := getStaticWord(value.Args[0])
				require.True(t, static)
				require.True(t, hasInvocationAt(plan, value.Pos().Line(), value.Pos().Col()))
			case *syntax.Redirect:
				if isFileRedirect(value.Op) {
					require.True(t, hasRedirectAt(plan, value.Pos().Line(), value.Pos().Col()))
				}
			}
			return true
		})
	})
}

func hasInvocationAt(plan Plan, line uint, column uint) bool {
	for _, invocation := range plan.Invocations {
		if invocation.Line == line && invocation.Column == column {
			return true
		}
	}
	return false
}

func hasRedirectAt(plan Plan, line uint, column uint) bool {
	for _, redirect := range plan.Redirects {
		if redirect.Line == line && redirect.Column == column {
			return true
		}
	}
	return false
}

func isFileRedirect(operator syntax.RedirOperator) bool {
	switch operator {
	case syntax.RdrIn, syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.RdrClob,
		syntax.AppClob, syntax.RdrAll, syntax.AppAll, syntax.RdrAllClob, syntax.AppAllClob:
		return true
	default:
		return false
	}
}
