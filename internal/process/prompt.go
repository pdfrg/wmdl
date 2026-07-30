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
