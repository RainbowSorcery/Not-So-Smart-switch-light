package com.example.myapplication

import android.os.Bundle
import android.util.Log
import android.widget.Button
import android.widget.Toast
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import org.eclipse.paho.client.mqttv3.MqttClient
import org.eclipse.paho.client.mqttv3.MqttConnectOptions
import org.eclipse.paho.client.mqttv3.MqttMessage
import org.eclipse.paho.client.mqttv3.persist.MemoryPersistence
import java.io.BufferedReader
import java.io.InputStreamReader
import java.io.PrintWriter
import java.net.InetSocketAddress
import java.net.Socket
import kotlin.concurrent.thread

class MainActivity : AppCompatActivity() {

    private var mqttClient: MqttClient? = null

    // 控制连接状态的变量
    @Volatile
    private var isConnected = false
    private var isRunning = true // 界面销毁时置为 false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContentView(R.layout.activity_main)

        // 启动后台连接与维护线程
        startConnectionThread()

        val myButton = findViewById<Button>(R.id.button)
        myButton.setOnClickListener {
            sendMessage("OFF")
        }

        val myButton2 = findViewById<Button>(R.id.button2)
        myButton2.setOnClickListener {
            sendMessage("ON")

        }
    }

    /**
     * 后台线程：负责初始连接和断线自动重连
     */
    private fun startConnectionThread() {
        thread {
            while (isRunning) {
                if (mqttClient == null || mqttClient?.isConnected != true) {
                    try {
                        Log.d("Mqtt", "正在尝试连接服务器...")

                        val brokerUrl = "tcp://192.168.0.104:1883"
                        val clientId = MqttClient.generateClientId()

                        mqttClient = MqttClient(brokerUrl, clientId, MemoryPersistence())

                        // 【关键点 1】：配置连接参数
                        val options = MqttConnectOptions().apply {
                            isCleanSession = true
                            connectionTimeout = 10
                            keepAliveInterval = 20
                        }

                        // 【关键点 2】：真正执行连接
                        mqttClient?.connect(options)



                        runOnUiThread {
                            Toast.makeText(this, "服务器已连接", Toast.LENGTH_SHORT).show()
                        }

                    } catch (e: Exception) {
                        Log.e("Socket", "连接失败，5秒后重试: ${e.message}")
                        isConnected = false
                        Thread.sleep(5000) // 等待5秒后重试
                    }
                }
                Thread.sleep(1000) // 每秒检查一次状态
            }
        }
    }



    /**
     * 发送消息的统一方法
     */
    private fun sendMessage(msg: String) {
        if (mqttClient?.isConnected != true) {
            Toast.makeText(this, "未连接到服务器，请稍后", Toast.LENGTH_SHORT).show()
            return
        }

        thread {
            try {
                val message = MqttMessage(msg.toByteArray()).apply {
                    qos = 0
                }
                mqttClient?.publish("esp8266/inTopic", message)
                runOnUiThread {
                    Toast.makeText(this, "发送 $msg 成功", Toast.LENGTH_SHORT).show()
                }
            } catch (e: Exception) {
                Log.e("Socket", "发送失败")
                closeSafe() // 发送失败通常意味着连接已断开
            }
        }
    }

    /**
     * 安全关闭资源
     */
    private fun closeSafe() {
        try {
            isConnected = false
            mqttClient?.close();
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        isRunning = false // 停止重连线程
        closeSafe()
    }
}