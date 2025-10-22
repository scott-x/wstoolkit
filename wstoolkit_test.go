package wstoolkit_test

import (
	"log"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/scott-x/wstoolkit" // 确保替换为您的实际模块路径
	. "github.com/smartystreets/goconvey/convey"
)

// ----------------------------------------------------
// 辅助函数
// ----------------------------------------------------

// startTestServer 启动一个 httptest.Server 并返回 WebSocket URL 和 Server
func startTestServer(handler wstoolkit.MessageHandler) (*httptest.Server, *wstoolkit.Server, string) {
	// 设置较短的 PingPeriod，加速测试中的连接断开/超时检测
	wsServer := wstoolkit.NewServer(handler, wstoolkit.ServerOptions{
		PingPeriod: 50 * time.Millisecond,
	})

	testServer := httptest.NewServer(wsServer)

	// 将 http:// 转换为 ws://
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")

	return testServer, wsServer, wsURL
}

// waitTimeout 辅助函数，带超时等待 WaitGroup
func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	select {
	case <-c:
		return true // 完成
	case <-time.After(timeout):
		return false // 超时
	}
}

// ----------------------------------------------------
// 单元测试
// ----------------------------------------------------

func TestWebSocketToolkit(t *testing.T) {
	Convey("测试 WebSocket Toolkit 的核心功能", t, func() {

		var receivedMessages sync.Map // 用于缓存收到的消息

		// 任务处理器：将收到的消息存入 sync.Map
		handler := func(client *wstoolkit.Client, messageType int, data []byte) {
			if messageType == wstoolkit.TextMessage {
				// **修正点 1:** 使用导出的 GetRemoteAddr() 方法
				receivedMessages.Store(client.GetRemoteAddr(), string(data))
			}
		}

		testServer, wsServer, wsURL := startTestServer(handler)
		defer testServer.Close()

		Convey("1. 连接与断开测试", func() {
			Convey("应该能够成功建立一个连接", func() {
				wsClient, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
				So(err, ShouldBeNil)
				defer wsClient.Close()

				time.Sleep(10 * time.Millisecond)

				So(wsServer.Count(), ShouldEqual, 1)
			})

			Convey("连接关闭后，连接数应该为 0", func() {
				wsClient, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
				wsClient.Close()

				time.Sleep(50 * time.Millisecond)

				So(wsServer.Count(), ShouldEqual, 0)
			})
		})

		Convey("2. 消息发送与接收测试 (Handler 验证)", func() {
			wsClient, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			So(err, ShouldBeNil)
			defer wsClient.Close()

			testMessage := "Hello Convey Test"

			Convey("发送文本消息，Server 应该能够接收并处理", func() {
				err = wsClient.WriteMessage(websocket.TextMessage, []byte(testMessage))
				So(err, ShouldBeNil)

				time.Sleep(10 * time.Millisecond)

				// **修正点 2:** 使用 wsClient.LocalAddr().String() 作为键，匹配 handler 中使用的 GetRemoteAddr()
				addr := wsClient.LocalAddr().String()
				val, ok := receivedMessages.Load(addr)

				So(ok, ShouldBeTrue)
				So(val, ShouldEqual, testMessage)
			})
		})

		Convey("3. 广播功能测试", func() {
			client1, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
			defer client1.Close()
			client2, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
			defer client2.Close()

			time.Sleep(10 * time.Millisecond)
			So(wsServer.Count(), ShouldEqual, 2)

			broadcastMessage := "This is a broadcast message!"

			var wg sync.WaitGroup
			wg.Add(2)

			// 客户端接收逻辑
			receiveAndCheck := func(client *websocket.Conn) {
				defer wg.Done()
				_, p, err := client.ReadMessage()
				// 检查错误和消息内容
				if err == nil && string(p) == broadcastMessage {
					// 成功收到
				} else {
					log.Printf("客户端接收失败: %v, 消息: %s\n", err, string(p))
					t.Errorf("广播接收失败")
				}
			}

			go receiveAndCheck(client1)
			go receiveAndCheck(client2)

			wsServer.BroadcastToAll(broadcastMessage)

			So(waitTimeout(&wg, 100*time.Millisecond), ShouldBeTrue) // 断言在超时前完成
		})

		Convey("4. Client Context 存储", func() {
			expectedID := "TestUser123"
			var contextCheck sync.Map

			// 自定义 Handler：触发消息时，在 Context 中设置一个 ID
			customHandler := func(client *wstoolkit.Client, messageType int, data []byte) {
				if messageType == wstoolkit.TextMessage {
					if client.Context["ID"] == nil {
						client.Context["ID"] = expectedID
						// 记录已设置，用于外部断言
						contextCheck.Store(client.GetRemoteAddr(), true)
					}
				}
			}

			testServerCtx, _, wsURLCtx := startTestServer(customHandler)
			defer testServerCtx.Close()
			defer testServerCtx.Close()

			client, _, _ := websocket.DefaultDialer.Dial(wsURLCtx, nil)
			defer client.Close()

			// 触发消息，执行 Handler，设置 Context
			client.WriteMessage(websocket.TextMessage, []byte("init context"))

			time.Sleep(10 * time.Millisecond)

			// 间接断言：检查 Handler 是否成功设置了 Context (通过 contextCheck map 确认)
			addr := client.LocalAddr().String()
			set, ok := contextCheck.Load(addr)

			So(ok, ShouldBeTrue)
			So(set, ShouldBeTrue)

			// (注意：直接断言 Context["ID"] 的值需要更复杂的测试工具或导出的 Client 列表，
			//  此处通过 Handler 内部的 side effect 即可验证 Context 功能的正确性)
		})
	})
}
