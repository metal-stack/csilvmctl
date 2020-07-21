package helper

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Prompt the user to given compare text
func Prompt(msg, compare string) error {
	fmt.Print(msg)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	text := scanner.Text()
	if strings.ToLower(text) != strings.ToLower(compare) {
		return fmt.Errorf("expected %s, aborting", compare)
	}
	return nil
}
