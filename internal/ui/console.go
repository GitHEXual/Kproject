package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorCyan   = "\033[36m"
	ColorGray   = "\033[90m"
	ColorBold   = "\033[1m"
)

type Console struct {
	mu sync.Mutex
}

func NewConsole() *Console {
	return &Console{}
}

// StartReader spawns a single goroutine that reads stdin line-by-line
// and sends trimmed lines into the returned channel. This must be the
// ONLY place in the program that reads from stdin.
func (c *Console) StartReader() <-chan string {
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			ch <- strings.TrimSpace(scanner.Text())
		}
		close(ch)
	}()
	return ch
}

func (c *Console) Print(msg string) {
	c.mu.Lock()
	fmt.Print(msg)
	c.mu.Unlock()
}

func (c *Console) Println(msg string) {
	c.mu.Lock()
	fmt.Println(msg)
	c.mu.Unlock()
}

func (c *Console) Printf(format string, args ...any) {
	c.mu.Lock()
	fmt.Printf(format, args...)
	c.mu.Unlock()
}

func (c *Console) ClearScreen() {
	c.mu.Lock()
	fmt.Print("\033[2J\033[H")
	c.mu.Unlock()
}

func (c *Console) ShowHeader(shortID string) {
	c.ClearScreen()
	c.Println(ColorBold + "╔══════════════════════════════════╗")
	c.Println("║       Kproject Messenger         ║")
	c.Println("╚══════════════════════════════════╝" + ColorReset)
	c.Println("")
	c.Printf("  %sВаш ID: %s%s%s\n\n", ColorYellow, ColorBold, shortID, ColorReset)
}

func (c *Console) ShowMenuOptions() {
	c.Println(ColorCyan + "  1" + ColorReset + " — Начать чат")
	c.Println(ColorCyan + "  2" + ColorReset + " — Выход")
	c.Println("")
	c.Print("  > ")
}

func (c *Console) ShowConnectStatus(msg string) {
	c.Printf("  %s► %s%s\n", ColorYellow, msg, ColorReset)
}

func (c *Console) ShowConnectError(msg string) {
	c.Printf("  %s✗ Ошибка: %s%s\n", ColorRed, msg, ColorReset)
}

func (c *Console) ShowConnectSuccess(peerID string, connType string) {
	c.Printf("\n  %s✓ Подключено к %s (%s)%s\n", ColorGreen, peerID, connType, ColorReset)
}

func (c *Console) ShowChatHeader(peerShortID string, connType string) {
	c.Printf("\n  %s--- Чат с %s [%s] ---%s\n", ColorBold, peerShortID, connType, ColorReset)
	c.Printf("  %sexit() — выход из чата%s\n\n", ColorGray, ColorReset)
}

func (c *Console) ShowChatPrompt() {
	c.Print("  > ")
}

func (c *Console) ShowChatMessage(from string, content string, isSelf bool) {
	if isSelf {
		c.Printf("  %s[Вы]%s %s\n", ColorGreen, ColorReset, content)
	} else {
		c.Printf("\r  %s[%s]%s %s\n  > ", ColorCyan, from, ColorReset, content)
	}
}

func (c *Console) ShowSystemMessage(msg string) {
	c.Printf("\r  %s%s%s\n", ColorGray, msg, ColorReset)
}

func (c *Console) ShowIncomingRequest(fromID string) {
	c.Printf("\n  %s⟵ Входящее подключение от %s%s\n", ColorCyan, fromID, ColorReset)
	c.Print("  Принять? (y/n): ")
}

func (c *Console) ShowAccepted() {
	c.Printf("  %s✓ Подключение принято%s\n", ColorGreen, ColorReset)
}

func (c *Console) ShowRejected() {
	c.Printf("  %sПодключение отклонено.%s\n\n", ColorGray, ColorReset)
}

func (c *Console) ShowChatEnded() {
	c.Printf("\n  %sЧат завершён.%s\n\n", ColorGray, ColorReset)
}

func (c *Console) ShowPeerDisconnected() {
	c.Printf("\r  %sСобеседник отключился.%s\n", ColorRed, ColorReset)
}

func (c *Console) ShowAskPeerID() {
	c.Print("  Введите ID собеседника: ")
}

func (c *Console) ShowPressEnter() {
	c.Println("\n  Нажмите Enter для возврата в меню.")
}
