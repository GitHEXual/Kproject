package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/GitHEXual/Kproject/internal/chat"
	"github.com/GitHEXual/Kproject/internal/node"
	"github.com/GitHEXual/Kproject/internal/registry"
	"github.com/GitHEXual/Kproject/internal/ui"
	"github.com/libp2p/go-libp2p/core/peer"
)

type incomingRequest struct {
	peerID     peer.ID
	responseCh chan bool
}

func main() {
	serverAddr := flag.String("server", "", "server multiaddr (required)")
	flag.Parse()

	if *serverAddr == "" {
		fmt.Fprintln(os.Stderr, "Usage: client -server <multiaddr>")
		fmt.Fprintln(os.Stderr, "Example: client -server /ip4/127.0.0.1/tcp/4001/p2p/12D3Koo...")
		os.Exit(1)
	}

	logFile, err := os.OpenFile("/tmp/kproject-client.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	con := ui.NewConsole()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	con.Println("\n  Подключение к серверу...")
	n, err := node.NewClientNode(ctx, *serverAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка подключения к серверу: %v\n", err)
		os.Exit(1)
	}
	defer n.Close()

	serverPeerID := n.Host.Network().Conns()[0].RemotePeer()
	regClient := registry.NewClient(n.Host, serverPeerID)

	if err := regClient.Register(ctx, n.ShortID, n.Addrs()); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка регистрации: %v\n", err)
		os.Exit(1)
	}

	chatSvc := chat.NewService(n.Host, n.ShortID)
	defer chatSvc.Close()

	stdinCh := con.StartReader()
	incomingCh := make(chan incomingRequest, 1)
	disconnectCh := make(chan peer.ID, 1)

	chatSvc.OnIncomingRequest = func(peerID peer.ID) bool {
		req := incomingRequest{
			peerID:     peerID,
			responseCh: make(chan bool, 1),
		}
		incomingCh <- req
		return <-req.responseCh
	}

	chatSvc.OnPeerDisconnect = func(peerID peer.ID) {
		select {
		case disconnectCh <- peerID:
		default:
		}
	}

	for {
		con.ShowHeader(n.ShortID)
		con.ShowMenuOptions()

		select {
		case line, ok := <-stdinCh:
			if !ok {
				return
			}
			switch line {
			case "1":
				handleOutgoingChat(con, stdinCh, disconnectCh, chatSvc, regClient, ctx, n.ShortID)
			case "2":
				con.Println("\n  Выход...")
				_ = regClient.Unregister(ctx)
				return
			}

		case req := <-incomingCh:
			peerShortID := req.peerID.String()[:8]
			con.ShowIncomingRequest(peerShortID)

			answer, ok := <-stdinCh
			if !ok {
				req.responseCh <- false
				return
			}

			accepted := answer == "y" || answer == "Y" || answer == "д" || answer == "Д"
			req.responseCh <- accepted

			if accepted {
				con.ShowAccepted()
				connType := chatSvc.ConnectionType(req.peerID)
				chatLoop(con, stdinCh, disconnectCh, chatSvc, n.ShortID, req.peerID, peerShortID, connType)
			} else {
				con.ShowRejected()
			}
		}
	}
}

func handleOutgoingChat(
	con *ui.Console,
	stdinCh <-chan string,
	disconnectCh chan peer.ID,
	chatSvc *chat.Service,
	regClient *registry.Client,
	ctx context.Context,
	myShortID string,
) {
	con.ShowAskPeerID()
	targetID, ok := <-stdinCh
	if !ok || targetID == "" {
		return
	}

	con.Println("")
	con.ShowConnectStatus("Поиск пользователя " + targetID + "...")

	info, err := regClient.Lookup(ctx, targetID)
	if err != nil {
		con.ShowConnectError(fmt.Sprintf("Пользователь не найден: %v", err))
		con.ShowPressEnter()
		<-stdinCh
		return
	}
	con.ShowConnectStatus(fmt.Sprintf("Найден: %s", info.ID.String()[:16]+"..."))

	statusCh := make(chan chat.ConnectStatus, 10)
	go func() {
		for status := range statusCh {
			if status.Error != nil {
				con.ShowConnectError(status.Error.Error())
				return
			}
			con.ShowConnectStatus(status.Message)
		}
	}()

	err = chatSvc.ConnectToPeer(ctx, info, statusCh)
	if err != nil {
		con.ShowConnectError(err.Error())
		con.ShowPressEnter()
		<-stdinCh
		return
	}

	connType := chatSvc.ConnectionType(info.ID)
	con.ShowConnectSuccess(targetID, connType)
	chatLoop(con, stdinCh, disconnectCh, chatSvc, myShortID, info.ID, targetID, connType)
}

func chatLoop(
	con *ui.Console,
	stdinCh <-chan string,
	disconnectCh chan peer.ID,
	chatSvc *chat.Service,
	myShortID string,
	peerID peer.ID,
	peerShortID string,
	connType string,
) {
	con.ShowChatHeader(peerShortID, connType)

	msgCh := make(chan chat.Message, 16)
	oldOnMessage := chatSvc.OnMessage
	chatSvc.OnMessage = func(msg chat.Message) {
		select {
		case msgCh <- msg:
		default:
		}
	}
	defer func() { chatSvc.OnMessage = oldOnMessage }()

	con.ShowChatPrompt()

	for {
		select {
		case line, ok := <-stdinCh:
			if !ok {
				return
			}
			if line == "exit()" {
				chatSvc.Disconnect(peerID)
				con.ShowChatEnded()
				return
			}
			if line == "" {
				con.ShowChatPrompt()
				continue
			}
			if err := chatSvc.SendMessage(peerID, line); err != nil {
				con.Printf("  %sОшибка отправки: %v%s\n", ui.ColorRed, err, ui.ColorReset)
				con.ShowChatPrompt()
				continue
			}
			con.ShowChatMessage(myShortID, line, true)
			con.ShowChatPrompt()

		case msg := <-msgCh:
			con.ShowChatMessage(msg.From, msg.Content, false)

		case dcPeer := <-disconnectCh:
			if dcPeer == peerID {
				con.ShowPeerDisconnected()
				con.ShowChatEnded()
				return
			}
		}
	}
}
