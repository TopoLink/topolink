package main

import (
	"fmt"
	"log"
	"os"

	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/report"
)

// render 按格式输出 findings。
func render(findings []engine.Finding, format string) {
	switch format {
	case "github":
		if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
			if err := appendFile(path, report.Markdown(findings)); err != nil {
				log.Printf("warning: write job summary: %v", err)
			}
		}
		for _, a := range report.Annotations(findings) {
			fmt.Println(a)
		}
		printFindings(findings)
	case "markdown":
		fmt.Print(report.Markdown(findings))
	default:
		printFindings(findings)
	}
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func printFindings(findings []engine.Finding) {
	if len(findings) == 0 {
		log.Println("no L1 findings")
		return
	}
	log.Printf("%d L1 finding(s):", len(findings))
	for _, f := range findings {
		loc := ""
		if f.Loc.File != "" {
			loc = fmt.Sprintf(" (%s:%d)", f.Loc.File, f.Loc.Line)
		}
		log.Printf("  [%s · %s] %s entry=%s%s", engine.RuleName(f.Rule), f.Level.Label(), f.Summary, f.EntryID, loc)
	}
}
