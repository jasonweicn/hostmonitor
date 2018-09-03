// main
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {

	// 接收系统信号
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, os.Kill)

	// 获取要监控的目标主机IP地址
	var hostip string
	fmt.Printf("Enter host ip address:")
	fmt.Scanln(&hostip)

	var (

		// 预警统计时间（秒）
		warning_time int64 = 120

		// 预警统计次数
		warning_num = 10
	)

	// 读取配置文件
	conf, err := readconfig("config.ini")
	fmt.Println(conf.conf["smtp"].Get("host"))

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
					fmt.Println("WARNING: Host network exception, Please check!")

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

type Config struct {
	conf map[string]url.Values
}

// 读取ini
func readconfig(filename string) (*Config, error) {

	cf := &Config{
		conf: make(map[string]url.Values, 10),
	}
	replacer := strings.NewReplacer(" ", "")
	f, _ := os.Open(filename)
	buf := bufio.NewReader(f)
	defer f.Close()

	tag := ""
	for {
		l, err := buf.ReadString('\n')
		if err != nil && err != errors.New("EOF") {
			//fmt.Println(err.Error())
			break
		}
		if l == "" {
			break
		}
		line := strings.TrimSpace(l)

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
			cf.conf[tag] = url.Values{}
		} else {
			line = replacer.Replace(line)
			spl := strings.Split(line, "=")

			if line[0:1] == ";" {
				continue
			}

			if len(spl) < 2 {
				return nil, errors.New("error:" + line)
			}
			cf.conf[tag].Set(strings.Replace(spl[0], ".", "_", -1), spl[1])
		}
	}

	return cf, nil
}

// 发送邮件
func sendmail() {
	fmt.Println("Send mail succeed.")
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
