#include <Servo.h>

#include <PubSubClient.h>
#include <ESP8266WiFi.h>
#include <ESP8266WebServer.h>
#include <WiFiUdp.h>


// --- 这里修改你的 WiFi 信息 ---
const char* ssid = "Ziroom706-2.5G";
const char* password = "4001001111";
unsigned long lastMsgTime = 0; // 用于非阻塞定时器

// 门磁 GPIO编号
const int doorSensorPin = 4; 


// mqtt borker
const char* mqtt_server = "192.168.0.104";

WiFiClient espClient;
PubSubClient mqttClient(espClient); 

Servo myServo;

int pin = 5;      // D1接口
int neutral = 90; // 中间休息位


void setup() {
  Serial.begin(115200); 
  Serial.println("");
  Serial.println("\n--- [ESP8266 启动成功] ---");

  // 连接 WiFi
  WiFi.begin(ssid, password);

  Serial.print("正在连接 WiFi");
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }

    // 连接成功，打印 IP 地址
  Serial.println("");
  Serial.print("连接成功！IP 地址: ");
  Serial.println(WiFi.localIP());
  WiFi.setAutoReconnect(true); // 开启自动重连模式
  WiFi.persistent(true);      // 将 WiFi 信息保存在 Flash 中，断电重连更稳
  bool success = WiFi.setSleepMode(WIFI_MODEM_SLEEP);
  if(success) {
      Serial.println("Modem Sleep 设置成功！");
  } else {
      Serial.println("Modem Sleep 设置失败，请检查 WiFi 是否已连接。");
  }

  // 【核心代码】：关闭 Wi-Fi 休眠，强迫芯片持续消耗约 70mA-80mA 的电流
  WiFi.setSleepMode(WIFI_NONE_SLEEP); 

  // 初始让它回到中间，方便你安装叶片
  myServo.attach(pin);
  myServo.write(neutral);
  delay(1000);
  myServo.detach(); 
  pinMode(doorSensorPin, INPUT_PULLUP);
  Serial.println("D5/GPIO14 门磁监控启动...");

  mqttClient.setServer(mqtt_server, 1883); // 设置服务器和端口
    // 【关键行】：绑定回调函数
  mqttClient.setCallback(callback); 

}

// 执行“按压”动作的通用函数
void press(int targetAngle) {
  myServo.attach(pin, 500, 2500); // 激活
  
  myServo.write(targetAngle);     // 1. 压下去
  delay(500);                     // 2. 停半秒，确保按实了
  
  myServo.write(neutral);         // 3. 缩回来，回到90度休息位
  delay(400);                     // 4. 等它回到位
  
  myServo.detach();               // 5. 彻底断电，防止发烫
}

void loop() {
  // 建议mqtt borker是否断开连接，如果断开了那么进行连接
  if (!mqttClient.connected()) {
      reconnect();
    }

  mqttClient.loop(); // 必须加上这一行！！
  // 2. 发送门磁状态
  int sensorStateCode = digitalRead(doorSensorPin);
  unsigned long now = millis();
  if (now - lastMsgTime >= 1000) {
    lastMsgTime = now; // 更新上一次发送的时间

    int sensorStateCode = digitalRead(doorSensorPin);
    if (sensorStateCode !=0) {
        mqttClient.publish("esp8266/door/state", "OPEN");
    }else {
        mqttClient.publish("esp8266/door/state", "CLOSE");
    }
  }

}


// 连接 MQTT 服务器并重连机制
void reconnect() {
  // 循环直到连接成功
  while (!mqttClient.connected()) {
    Serial.print("Attempting MQTT connection...");
    // 尝试连接 (参数是 ClientID)
    if (mqttClient.connect("ESP8266Client_UniqueName")) {
      Serial.println("connected");
      // 连接成功后订阅主题
      mqttClient.subscribe("esp8266/inTopic");
      mqttClient.subscribe("esp8266/control");
    } else {
      Serial.print("failed, rc=");
      Serial.print(mqttClient.state());
      Serial.println(" try again in 5 seconds");
      delay(5000);
    }
  }
}




// 当收到订阅的消息时，会自动触发这个函数
void callback(char* topic, byte* payload, unsigned int length) {
  Serial.print("收到消息 [");
  Serial.print(topic);
  Serial.print("] ");
  

  // 将收到的内容转为字符串
  String message = "";
  for (int i = 0; i < length; i++) {
    message += (char)payload[i];
  }
  Serial.println(message);
  if (strcmp(topic, "esp8266/control") == 0) {
      Serial.println("收到复位指令");
    if (message == "RESTART") {
      Serial.println("收到远程复位指令...");
      ESP.restart();
    }
  } else {
    // --- 根据消息内容执行动作 ---
    if (message == "OFF") {
      Serial.println("MQTT指令：开灯");
      press(115); // 执行你之前的舵机动作
    } 
    else if (message == "ON") {
      Serial.println("MQTT指令：关灯");
      press(58);
    }
  }
}

