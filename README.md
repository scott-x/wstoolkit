# wstoolkit

websocket toolkit 

### 快速开始

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/scott-x/wstoolkit" 
)

// ----------------------------------------------------
// 1. 实现业务逻辑 (Message Handler)
// ----------------------------------------------------
func handleMessage(client *wstoolkit.Client, messageType int, data []byte) {
	if messageType == wstoolkit.TextMessage {
		msg := string(data)
		
		// 第一次连接，设置一个 ID 上下文
		if _, ok := client.Context["ID"]; !ok {
			client.Context["ID"] = fmt.Sprintf("User-%d", time.Now().UnixNano()/1000000)
		}
		clientID := client.Context["ID"].(string)

		log.Printf("客户端[%s] 消息: %s\n", clientID, msg)
		
		// 1.1. 回显给发送者
		client.SendText("Server ACK: " + msg)

		// 1.2. 广播给所有人 (除了自己，如果需要)
		server.BroadcastToAll(fmt.Sprintf("[%s] 说: %s", clientID, msg))
	}
}

var server *wstoolkit.Server

func main() {
	// 2. 配置 Server 选项 (可选，使用零值会采用默认配置)
	opts := wstoolkit.ServerOptions{
		PingPeriod: 15 * time.Second, // 较快的心跳检测
	}

	// 3. 初始化 WebSocket Server
	server = wstoolkit.NewServer(handleMessage, opts)

	// 4. 注册到 HTTP 路由器
	http.Handle("/ws", server)
	
	// 5. 启动一个 Goroutine 定时广播服务器状态
	go func() {
		for {
			time.Sleep(time.Second * 10)
			count := server.Count()
			server.BroadcastToAll(fmt.Sprintf("Server Status: Current connections: %d", count))
		}
	}()

	log.Println("WebSocket Server 启动在 :8080. Endpoint: /ws")
	// 6. 启动 HTTP 监听
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("ListenAndServe 错误:", err)
	}
}
```

### 测试

```bash
go test -v ./...
```


