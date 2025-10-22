package wstoolkit

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 定义常量
const (
	// 定义消息类型
	TextMessage   = websocket.TextMessage
	BinaryMessage = websocket.BinaryMessage
)

// MessageHandler 定义了处理接收到消息的函数的签名。
// data 是消息内容，messageType 是 websocket.TextMessage 或 websocket.BinaryMessage。
type MessageHandler func(client *Client, messageType int, data []byte)

// Client 封装了一个 WebSocket 连接
type Client struct {
	conn *websocket.Conn
	// Context 允许用户存储与此连接相关的自定义数据 (例如用户ID, Session信息)
	Context map[string]interface{}
}

// GetRemoteAddr 返回客户端的远程网络地址，用于在测试中标识客户端。
func (c *Client) GetRemoteAddr() string {
	return c.conn.RemoteAddr().String()
}

// SendText 向此客户端发送文本消息
func (c *Client) SendText(message string) error {
	return c.conn.WriteMessage(TextMessage, []byte(message))
}

// SendBinary 向此客户端发送二进制消息
func (c *Client) SendBinary(data []byte) error {
	return c.conn.WriteMessage(BinaryMessage, data)
}

// Close 关闭当前客户端连接
func (c *Client) Close() {
	// 发送一个 CloseMessage 给客户端 (可选，但推荐)
	c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "server closing"))
	c.conn.Close()
}

// Server 封装了 WebSocket 服务器的核心逻辑和连接池管理
type Server struct {
	upgrader    websocket.Upgrader
	handler     MessageHandler
	connections map[*Client]bool // 存储所有活跃的客户端连接
	mu          sync.RWMutex     // 用于保护 connections 映射的读写锁
	pingPeriod  time.Duration
}

// ServerOptions 用于配置 Server 行为
type ServerOptions struct {
	ReadBufferSize  int
	WriteBufferSize int
	CheckOriginFunc func(r *http.Request) bool // 用于跨域控制
	PingPeriod      time.Duration              // Ping/Pong 心跳间隔，默认 30s
}

// NewServer 使用给定的消息处理器和选项创建并返回一个新的 Server 实例
func NewServer(handler MessageHandler, opts ServerOptions) *Server {
	// 如果未设置，提供默认值
	if opts.ReadBufferSize == 0 {
		opts.ReadBufferSize = 1024
	}
	if opts.WriteBufferSize == 0 {
		opts.WriteBufferSize = 1024
	}
	if opts.CheckOriginFunc == nil {
		opts.CheckOriginFunc = func(r *http.Request) bool { return true } // 默认允许所有源
	}
	if opts.PingPeriod == 0 {
		opts.PingPeriod = 30 * time.Second
	}

	return &Server{
		upgrader: websocket.Upgrader{
			ReadBufferSize:  opts.ReadBufferSize,
			WriteBufferSize: opts.WriteBufferSize,
			CheckOrigin:     opts.CheckOriginFunc,
		},
		handler:     handler,
		connections: make(map[*Client]bool),
		pingPeriod:  opts.PingPeriod,
	}
}

// ServeHTTP 是一个标准的 http.HandlerFunc，用于处理 WebSocket 升级请求。
// 开发者只需要将其注册到 http.HandleFunc 或路由框架中即可。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade 失败: %v\n", err)
		return
	}

	client := &Client{
		conn:    conn,
		Context: make(map[string]interface{}),
	}
	s.addClient(client)
	log.Printf("[WS] 客户端连接成功。当前连接数: %d\n", s.Count())

	// 启动 Goroutine 监听消息和处理心跳
	go s.listen(client)
	go s.ping(client)
}

// listen 持续监听客户端连接，接收和处理消息
func (s *Server) listen(client *Client) {
	defer func() {
		s.removeClient(client)
		client.conn.Close()
		log.Printf("[WS] 客户端连接已断开。当前连接数: %d\n", s.Count())
	}()

	// 设置一个合理的读超时，与心跳间隔相关
	client.conn.SetReadLimit(512)
	client.conn.SetReadDeadline(time.Now().Add(s.pingPeriod + 10*time.Second))

	// 设置 Pong 处理器，用于接收客户端的 Pong 响应，以更新读超时
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(s.pingPeriod + 10*time.Second))
		return nil
	})

	for {
		messageType, p, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WS] 客户端读取错误: %v\n", err)
			}
			break
		}
		// 调用外部定义的消息处理器
		if s.handler != nil {
			s.handler(client, messageType, p)
		}
	}
}

// ping 定时发送 Ping 帧，维持连接活跃
func (s *Server) ping(client *Client) {
	ticker := time.NewTicker(s.pingPeriod)
	defer func() {
		ticker.Stop()
		client.Close() // 确保在 ping 循环结束时关闭连接
	}()

	for range ticker.C {
		// 检查连接是否还在 Server 的连接池中
		if !s.IsActive(client) {
			return
		}

		// 发送 Ping 消息
		if err := client.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(s.pingPeriod/2)); err != nil {
			log.Printf("[WS] 客户端 Ping 失败: %v\n", err)
			return // Ping 失败，认为连接已死，退出
		}
	}
}

// --- 连接管理与广播 API ---

func (s *Server) addClient(client *Client) {
	s.mu.Lock()
	s.connections[client] = true
	s.mu.Unlock()
}

func (s *Server) removeClient(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 直接调用 delete，Go 会安全地处理 key 不存在的情况
	delete(s.connections, client)
}

// Count 返回当前活跃的连接数
func (s *Server) Count() int {
	s.mu.RLock()
	count := len(s.connections)
	s.mu.RUnlock()
	return count
}

// IsActive 检查客户端是否在活跃连接列表中
func (s *Server) IsActive(client *Client) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.connections[client]
	return ok
}

// BroadcastToAll 向所有活跃客户端发送文本消息
func (s *Server) BroadcastToAll(message string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for client := range s.connections {
		// 在 Goroutine 中发送，避免一个慢客户端阻塞整个广播
		go func(c *Client) {
			if err := c.SendText(message); err != nil {
				// 发送失败可能是连接即将关闭，让 read loop 去处理断开
				// log.Printf("[WS] 广播发送失败给客户端: %v\n", err)
			}
		}(client)
	}
}

// BroadcastBinaryToAll 向所有活跃客户端发送二进制消息
func (s *Server) BroadcastBinaryToAll(data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for client := range s.connections {
		go func(c *Client) {
			if err := c.SendBinary(data); err != nil {
				// log.Printf("[WS] 广播二进制发送失败给客户端: %v\n", err)
			}
		}(client)
	}
}
