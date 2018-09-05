// main
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {

	var (
		// 目标主机ip
		hostip string = ""

		// 邮件信息
		mail = make(map[string]string)

		// 预警统计时间（秒）
		warning_time int64 = 120

		// 预警统计次数
		warning_num = 10
	)

	// 接收系统信号
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, os.Kill)

	// 获取要监控的目标主机IP地址
	fmt.Printf("Enter host ip address:")
	fmt.Scanln(&hostip)

	// 读取配置文件
	conf, err := readconfig("config.ini")
	if err != nil {
		fmt.Println("load config.ini fail.")
	}

	mail["smtp_host"] = conf["smtp"]["host"]
	mail["smtp_port"] = conf["smtp"]["port"]
	mail["smtp_user"] = conf["smtp"]["username"]
	mail["smtp_passwd"] = conf["smtp"]["password"]
	mail["from"] = conf["mail"]["from"]
	mail["to"] = conf["mail"]["to"]

	//fmt.Println(conf)

	type ICMP struct {
		Type        uint8
		Code        uint8
		Checksum    uint16
		Identifier  uint16
		SequenceNum uint16
	}

	var icmp ICMP

	laddr := net.IPAddr{IP: net.ParseIP("0.0.0.0")}
	raddr := net.IPAddr{IP: net.ParseIP(hostip)}

	conn, err := net.DialIP("ip4:icmp", &laddr, &raddr)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	defer conn.Close()

	stoprun := false

	go func() {
		s := <-c
		fmt.Println("Got signal: ", s)
		if err = conn.Close(); err != nil {
			fmt.Println(err)
		}
		stoprun = true
	}()

	icmp.Type = 8
	icmp.Code = 0
	icmp.Checksum = 0
	icmp.Identifier = 0
	icmp.SequenceNum = 0

	var buffer bytes.Buffer
	binary.Write(&buffer, binary.BigEndian, icmp)
	icmp.Checksum = CheckSum(buffer.Bytes())
	buffer.Reset()
	binary.Write(&buffer, binary.BigEndian, icmp)

	recv := make([]byte, 1024)

	// 超时计数器
	timeout_num := 0

	// 超时日志
	timeout_log := make([]int64, warning_num)

	for {
		if _, err := conn.Write(buffer.Bytes()); err != nil {
			fmt.Println(err.Error())
			return
		}

		// 开始时间
		t_start := time.Now()

		// 设定超时时间
		conn.SetDeadline((time.Now().Add(time.Second * 5)))

		_, err := conn.Read(recv)

		// 出现超时
		if err != nil {

			// 记录本次超时出现的时间
			timeout_log[timeout_num] = time.Now().Unix()
			fmt.Println(timeout_log)

			fmt.Printf("PING %s : timeout...(%d)\n", hostip, timeout_num)

			if timeout_log[warning_num-1] > 0 {

				// 计算从第1次到第warning_num次超时之间的间隔时间
				var interval int64 = timeout_log[warning_num-1] - timeout_log[0]

				// 判断是否处于统计周期内
				if interval <= warning_time {
					mail["subject"] = "WARNING: Host(" + hostip + ") network exception"
					mail["body"] = "WARNING: Host(" + hostip + ") network exception, Please check it!"

					fmt.Println(mail["subject"])

					result := sendmail(mail)
					if result {
						fmt.Println("Send mail succeed.")
					} else {
						fmt.Println("Send mail fail.")
					}

					// 超时计数器归零
					timeout_num = 0

					// 超时日志数组元素全部归零
					for i := 0; i < warning_num; i++ {
						timeout_log[i] = 0
					}
				} else {

					// 从超时日志数组中移除超出统计范围的元素，其他元素前移
					j := 0
					for i := 0; i < warning_num; i++ {
						if timeout_log[i] == 0 {
							break
						}
						if timeout_log[warning_num-1]-timeout_log[i] > warning_time {
							timeout_log[i] = 0
							continue
						} else {
							timeout_log[j] = timeout_log[i]
							timeout_log[i] = 0
							j++
						}
					}
				}
			} else {
				// 超时计数器+1
				timeout_num++
			}

			// 判断是否结束进程
			if stoprun {
				return
			}

			// 重连
			conn, err = net.DialIP("ip4:icmp", &laddr, &raddr)
			if err != nil {
				fmt.Println(err.Error())
				return
			}
			defer conn.Close()

			continue
		}

		// 结束时间
		t_end := time.Now()

		dur := t_end.Sub(t_start).Nanoseconds() / 1e6

		fmt.Printf("PING %s : time = %dms\n", hostip, dur)

		time.Sleep(time.Second)
	}
}

// 读取ini
func readconfig(filename string) (map[string]map[string]string, error) {

	cf := make(map[string]map[string]string)

	replacer := strings.NewReplacer(" ", "")
	f, _ := os.Open(filename)
	buf := bufio.NewReader(f)
	defer f.Close()

	tag := ""

	for {
		line, err := buf.ReadString('\n')
		if err != nil && err != errors.New("EOF") {
			if line == "" {
				break
			}
		}
		line = strings.TrimSpace(line)

		// 长度为零继续循环
		if len(line) == 0 {
			continue
		}

		// 匹配[xxx]
		if idx := strings.Index(line, "["); idx != -1 {
			if line[len(line)-1:] != "]" {
				return nil, errors.New("Error: field to parse this symbol style:\"" + line + "\"")
			}
			tag = line[1 : len(line)-1]
			cf[tag] = make(map[string]string)
		} else {
			line = replacer.Replace(line)
			spl := strings.Split(line, "=")

			if line[0:1] == ";" {
				continue
			}

			if len(spl) < 2 {
				return nil, errors.New("error:" + line)
			}
			k := strings.Replace(spl[0], ".", "_", -1)
			cf[tag][k] = spl[1]
		}
	}

	return cf, nil
}

// 发送邮件
func sendmail(mail map[string]string) bool {
	b64 := base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/")

	header := make(map[string]string)
	header["From"] = mail["from"]
	header["To"] = mail["to"]
	header["Subject"] = fmt.Sprintf("=?UTF-8?B?%s?=", b64.EncodeToString([]byte(mail["subject"])))
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = "text/html; charset=UTF-8"
	header["Content-Transfer-Encoding"] = "base64"

	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + b64.EncodeToString([]byte(mail["body"]))

	auth := smtp.PlainAuth("text/plain", mail["smtp_user"], mail["smtp_passwd"], mail["smtp_host"])

	err := smtp.SendMail(mail["smtp_host"]+":"+mail["smtp_port"], auth, mail["from"], []string{mail["to"]}, []byte(message))
	if err != nil {
		return false
	}

	return true
}

func CheckSum(data []byte) uint16 {
	var (
		sum    uint32
		length int = len(data)
		index  int
	)
	for length > 1 {
		sum += uint32(data[index])<<8 + uint32(data[index+1])
		index += 2
		length -= 2
	}
	if length > 0 {
		sum += uint32(data[index])
	}
	sum += (sum >> 16)

	return uint16(^sum)
}
