package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

func main() {
	// 创建消息池
	messagePool := pool.New(1024, 1600)

	// 创建UDP监听器用于设备发现
	l, err := coapnet.NewListenUDP("udp4", "")
	if err != nil {
		log.Fatal(err)
	}

	// 创建UDP服务器
	timeout := time.Second * 30
	s := udp.NewServer(options.WithTransmission(1, timeout/2, 2), options.WithMessagePool(messagePool))

	var wg sync.WaitGroup
	defer wg.Wait()
	defer s.Stop()

	// 启动服务器
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Serve(l)
	}()

	// 创建发现请求上下文
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 用于存储发现的第一个设备地址
	var firstDevice string
	var deviceFound = make(chan bool)

	// 创建发现请求
	token, err := message.GetToken()
	if err != nil {
		log.Fatal("cannot get token:", err)
	}

	req := messagePool.AcquireMessage(ctx)
	defer messagePool.ReleaseMessage(req)

	err = req.SetupGet("/oic/res", token)
	if err != nil {
		log.Fatal("cannot create discover request:", err)
	}
	req.SetMessageID(message.GetMID())
	req.SetType(message.NonConfirmable)

	// 发送发现请求
	log.Println("Discovering devices...")
	err = s.DiscoveryRequest(req, "224.0.1.187:5683", func(cc *client.Conn, resp *pool.Message) {
		addr := cc.RemoteAddr().String()
		if firstDevice == "" {
			firstDevice = addr
			deviceFound <- true
		}
		log.Printf("Discovered device at: %v\n", addr)
	})
	if err != nil {
		log.Fatal("discovery error:", err)
	}

	// 等待发现至少一个设备
	<-deviceFound
	log.Printf("Selected device: %v\n", firstDevice)

	// 修改端口为5688用于observer连接
	host, _, err := net.SplitHostPort(firstDevice)
	if err != nil {
		log.Fatal("Error parsing address:", err)
	}
	observerAddr := fmt.Sprintf("%s:5688", host)

	// 连接到选定的设备
	ctx, cancel = context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	co, err := udp.Dial(observerAddr,
		options.WithNetwork("udp4"),
		options.WithTransmission(1, time.Second*5, 2),
	)
	if err != nil {
		log.Fatal("Error dialing:", err)
	}
	defer co.Close()

	// 设置观察处理函数
	log.Println("Starting observation...")
	obs, err := co.Observe(ctx, "/observe", func(req *pool.Message) {
		log.Printf("Received message from %v\n", firstDevice)
		body, err := req.ReadBody()
		if err != nil {
			log.Printf("Error reading body: %v", err)
			return
		}
		log.Printf("Message body: %s\n", string(body))
	})
	if err != nil {
		log.Fatal("Observe error:", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		obs.Cancel(ctx)
	}()

	// 保持程序运行
	fmt.Println("Press Ctrl+C to exit")
	select {}
}
