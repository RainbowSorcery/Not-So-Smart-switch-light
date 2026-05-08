package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	qdb "github.com/questdb/go-questdb-client/v3"
	"go.bug.st/serial"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	SaveInterval = 1 * time.Second // 数据库写入频率控制：1秒1次
)

var idleTime time.Time

func main() {
	// --- 1. 日志与硬件初始化 ---
	log.SetOutput(io.MultiWriter(os.Stdout, &lumberjack.Logger{Filename: "./radar.log", MaxSize: 5}))

	// --- 2. 串口初始化 (256000 波特率) ---
	port, err := serial.Open("/dev/ttyS7", &serial.Mode{BaudRate: 256000})
	if err != nil {
		log.Fatalf("❌ 串口打开失败: %v", err)
	}
	defer port.Close()

	// --- 3. 数据库与 MQTT 连接 ---
	ctx := context.Background()
	sender, err := qdb.NewLineSender(ctx, qdb.WithTcp(), qdb.WithAddress("127.0.0.1:9009"))
	if err != nil {
		log.Printf("⚠️ 数据库连接异常: %v", err)
	}

	option := mqtt.NewClientOptions().
		AddBroker("tcp://127.0.0.1:1883").
		SetAutoReconnect(true).                   // 显式开启自动重连
		SetMaxReconnectInterval(5 * time.Second). // 最大重连时间间隔
		SetConnectRetry(true)

	// --- 4. 激活雷达工程模式 ---
	setupRadarMode(port)
	log.Println("📡 系统启动：人体感应 + 环境光感监测中...")

	var lastSaveTime time.Time

	// 雷达检测状态
	statusStr := "未知"

	// 光感值
	lightLevel := int16(-1)
	lastDoorState := ""
	option.OnConnect = func(c mqtt.Client) {
		log.Println("✅ MQTT 连接成功，正在订阅主题...")
		// 将订阅逻辑放在这里，确保每次重连后都会重新订阅
		c.Subscribe("esp8266/door/state", 0, func(client mqtt.Client, msg mqtt.Message) {
			// 如果门开了且没检测到关 那么就开灯
			currentDoorState := string(msg.Payload())
			if currentDoorState != lastDoorState && lightLevel == 0 {
				log.Printf("检测到不同状态，上次状态:%s, 当前状态:%s, 雷达状态:%s", lastDoorState, currentDoorState, statusStr)
				lastDoorState = currentDoorState
				if currentDoorState == "OPEN" && statusStr != "无人" {
					log.Printf("开门未检测到光，开灯, 光敏:%d, 当前门状态:%s, 上次门状态:%s,雷达检测状态:%s", lightLevel, currentDoorState, lastDoorState, statusStr)
					client.Publish("esp8266/inTopic", 0, false, "ON")
				}
			}
		})
	}

	option.OnConnectionLost = func(c mqtt.Client, err error) {
		log.Printf("❌ MQTT 连接断开: %v", err)
	}

	mqttClient := mqtt.NewClient(option)
	token := mqttClient.Connect()

	if token.Wait() && token.Error() != nil {
		log.Printf("⚠️ MQTT 初始连接失败（将在后台重连）: %v", token.Error())
	}

	for {
		// 搜索帧头 F4 F3 F2 F1
		header := make([]byte, 4)
		if _, err := io.ReadFull(port, header); err != nil {
			continue
		}
		if header[0] != 0xF4 || header[1] != 0xF3 || header[2] != 0xF2 || header[3] != 0xF1 {
			continue
		}

		// 读取 Payload 长度
		lenBuf := make([]byte, 2)
		io.ReadFull(port, lenBuf)
		dataLen := binary.LittleEndian.Uint16(lenBuf)

		// 读取 Payload 数据 + 4字节帧尾
		allData := make([]byte, dataLen+4)
		io.ReadFull(port, allData)

		// 校验帧尾 F8 F7 F6 F5
		if allData[dataLen] == 0xF8 && allData[dataLen+3] == 0xF5 {
			p := allData[:dataLen]

			// --- 数据解析 ---
			statusNames := []string{"无人", "发现运动", "发现静止", "动静皆有"}
			if int(p[2]) < len(statusNames) {
				fmt.Printf(">>检测状态:%x\n", p[2])
				statusStr = statusNames[p[2]]
			}

			detectDist := binary.LittleEndian.Uint16(p[9:11]) // 综合探测距离

			// --- 提取光敏信息 (工程模式下 Payload 第30个字节) ---
			if p[0] == 0x01 && len(p) >= 30 {
				lightLevel = int16(p[31])
			}

			fmt.Printf(">>帧内数据:%x\n", p)

			// --- 终端实时回显 ---
			fmt.Printf(">> 状态:%-8s | 距离:%4dcm | 光感:%3d | 存储倒计时: %.1fs \n",
				statusStr, detectDist, lightLevel, (SaveInterval - time.Since(lastSaveTime)).Seconds())

			//fmt.Print("\033[3A") // \033[2A 表示光标向上移动 3 行

			// --- 存入数据库 (限速逻辑) ---
			if sender != nil && time.Since(lastSaveTime) >= SaveInterval {
				row := sender.Table("radar_data").
					Symbol("status", statusStr).
					Int64Column("light_level", int64(lightLevel)).
					Int64Column("detect_dist", int64(detectDist))

				// 判断是否满足无人或睡着了可以自动熄灯
				HandleStatus(statusStr, mqttClient, lightLevel)

				// 如果是工程模式，记录 0-8 门的能量细节
				if p[0] == 0x01 && len(p) >= 29 {
					for i := 0; i <= 8; i++ {
						row.Int64Column(fmt.Sprintf("m_gate_%d", i), int64(p[11+i])) // 运动能量
						row.Int64Column(fmt.Sprintf("s_gate_%d", i), int64(p[20+i])) // 静止能量
					}
				}

				if err := sender.At(ctx, time.Now()); err == nil {
					sender.Flush(ctx)
					lastSaveTime = time.Now()
				}
			}
		}
	}
}

func setupRadarMode(port serial.Port) {
	cmd := [][]byte{
		{0xFD, 0xFC, 0xFB, 0xFA, 0x04, 0x00, 0xFF, 0x00, 0x01, 0x00, 0x04, 0x03, 0x02, 0x01}, // 开启配置
		{0xFD, 0xFC, 0xFB, 0xFA, 0x02, 0x00, 0x62, 0x00, 0x04, 0x03, 0x02, 0x01},             // 开启工程模式
		{0xFD, 0xFC, 0xFB, 0xFA, 0x02, 0x00, 0xFE, 0x00, 0x04, 0x03, 0x02, 0x01},             // 结束配置
	}
	for _, c := range cmd {
		port.Write(c)
		time.Sleep(200 * time.Millisecond)
	}
	err := port.ResetInputBuffer()
	if err != nil {
		return
	}
}

var mu sync.Mutex

func HandleStatus(state string, mqttClient mqtt.Client, lightLevel int16) {
	mu.Lock()
	defer mu.Unlock()

	if idleTime.IsZero() {
		idleTime = time.Now()
	}

	// 如果雷达检测静止状态超过十分钟表示已经睡着了，需要熄灯
	if state == "无人" && lightLevel != 0 {
		duration := time.Now().Sub(idleTime)
		minutes := duration.Minutes()

		now := time.Now()
		hour := now.Hour()
		// 再 18:00 ~ 06:00之间才会自动关灯，其他时间段不做处理
		if hour >= 18 || hour < 6 {
			if minutes >= 30 {
				log.Printf("已经进入静止状态30分钟，判断人已睡着，准备关灯, 光敏:%d", lightLevel)
				idleTime = time.Now()

				mqttClient.Publish("esp8266/inTopic", 0, false, "OFF")
				time.Sleep(1 * time.Second)
			}
		}
	} else {
		idleTime = time.Now()
		fmt.Printf("非静止状态，更新状态时间\n")
		time.Sleep(200 * time.Millisecond)
	}
}
