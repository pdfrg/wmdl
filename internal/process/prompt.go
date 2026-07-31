package process

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

func PromptYesNo(ctx context.Context, prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		ch <- scanner.Text()
	}()
	select {
	case ans := <-ch:
		return strings.ToLower(strings.TrimSpace(ans)) == "y" || strings.ToLower(strings.TrimSpace(ans)) == "yes"
	case <-ctx.Done():
		return false
	}
}

type RetryAction int

const (
	RetryActionSkip RetryAction = iota
	RetryActionRetry
	RetryActionQuit
)

// PromptTwo presents a two-option choice. Matches on first letter (case-insensitive)
// or the full option string. Returns optA/optB, or defaultOpt on empty/ctx cancel.
func PromptTwo(ctx context.Context, prompt, optA, optB, defaultOpt string) string {
	keyA := strings.ToLower(string(optA[0]))
	keyB := strings.ToLower(string(optB[0]))
	lowA := strings.ToLower(optA)
	lowB := strings.ToLower(optB)
	for {
		fmt.Print(prompt, " ")
		ch := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			ch <- scanner.Text()
		}()
		select {
		case ans := <-ch:
			ans = strings.ToLower(strings.TrimSpace(ans))
			if ans == "" {
				return defaultOpt
			}
			if ans == keyA || ans == lowA {
				return optA
			}
			if ans == keyB || ans == lowB {
				return optB
			}
		case <-ctx.Done():
			return defaultOpt
		}
	}
}

func PromptRetry(ctx context.Context, label string, err error) RetryAction {
	fmt.Fprintf(os.Stderr, "  %s error: %v\n", label, err)
	for {
		fmt.Fprintf(os.Stderr, "    [r] retry  [s] skip this item  [q] quit pipeline\n")
		fmt.Fprintf(os.Stderr, "  Choose: ")
		ch := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			ch <- scanner.Text()
		}()
		select {
		case ans := <-ch:
			switch strings.ToLower(strings.TrimSpace(ans)) {
			case "r", "retry":
				return RetryActionRetry
			case "s", "skip":
				return RetryActionSkip
			case "q", "quit":
				return RetryActionQuit
			}
		case <-ctx.Done():
			return RetryActionQuit
		}
	}
}
