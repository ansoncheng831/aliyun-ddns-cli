package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/denverdino/aliyungo/common"
	dns "github.com/honwen/aliyun-ddns-cli/alidns"
	"github.com/urfave/cli"
)

// AccessKey from https://ak-console.aliyun.com/#/accesskey
type AccessKey struct {
	ID     string
	Secret string
	client *dns.Client
}

func (ak *AccessKey) getClient() *dns.Client {
	if len(ak.ID) <= 0 && len(ak.Secret) <= 0 {
		return nil
	}
	if ak.client == nil {
		ak.client = dns.NewClient(ak.ID, ak.Secret)
		ak.client.SetEndpoint(dns.DNSDefaultEndpointNew)
	}
	return ak.client
}

func (ak AccessKey) String() string {
	return fmt.Sprintf("Access Key: [ ID: %s ;\t Secret: %s ]", ak.ID, ak.Secret)
}

func (ak *AccessKey) ListRecord(domain string) (dnsRecords []dns.RecordTypeNew, err error) {
	if strings.Count(domain, `.`) != 1 {
		_, domain = splitDomain(domain)
	}
	var resp *dns.DescribeDomainRecordsNewResponse
	for idx := 1; idx <= 99; idx++ {
		resp, err = ak.getClient().DescribeDomainRecordsNew(
			&dns.DescribeDomainRecordsNewArgs{
				DomainName: domain,
				Pagination: common.Pagination{PageNumber: idx, PageSize: 50},
			})
		if err != nil {
			return
		}
		dnsRecords = append(dnsRecords, resp.DomainRecords.Record...)
		if len(dnsRecords) >= resp.PaginationResult.TotalCount {
			return
		}
	}
	return
}

func (ak *AccessKey) DelRecord(rr, domain string) (err error) {
	var target *dns.RecordTypeNew
	if dnsRecords, err := ak.ListRecord(domain); err == nil {
		for i := range dnsRecords {
			if dnsRecords[i].RR == rr {
				target = &dnsRecords[i]
				_, err = ak.getClient().DeleteDomainRecord(
					&dns.DeleteDomainRecordArgs{
						RecordId: target.RecordId,
					},
				)
			}
		}
	} else {
		return err
	}
	return
}

func (ak *AccessKey) UpdateRecord(recordID, rr, dmType, value string) (err error) {
	_, err = ak.getClient().UpdateDomainRecord(
		&dns.UpdateDomainRecordArgs{
			RecordId: recordID,
			RR:       rr,
			Value:    value,
			Type:     dmType,
		})
	return
}

func (ak *AccessKey) AddRecord(domain, rr, dmType, value string) (err error) {
	_, err = ak.getClient().AddDomainRecord(
		&dns.AddDomainRecordArgs{
			DomainName: domain,
			RR:         rr,
			Type:       dmType,
			Value:      value,
		})
	return err
}

func (ak *AccessKey) CheckAndUpdateRecord(rr, domain, ipaddr string, ipv6 bool) (err error) {
	fulldomain := strings.Join([]string{rr, domain}, `.`)
	if getDNS(fulldomain, ipv6) == ipaddr {
		return // Skip
	}
	recordType := "A"
	if ipv6 {
		recordType = "AAAA"
	}
	targetCnt := 0
	var target *dns.RecordTypeNew
	if dnsRecords, err := ak.ListRecord(domain); err == nil {
		for i := range dnsRecords {
			if dnsRecords[i].RR == rr && dnsRecords[i].Type == recordType {
				target = &dnsRecords[i]
				targetCnt++
			}
		}
	} else {
		return err
	}

	if targetCnt > 1 {
		ak.DelRecord(rr, domain)
		target = nil
	}

	if target == nil {
		err = ak.AddRecord(domain, rr, recordType, ipaddr)
	} else if target.Value != ipaddr {
		if target.Type != recordType {
			return fmt.Errorf("record type error! oldType=%s, targetType=%s", target.Type, recordType)
		}
		err = ak.UpdateRecord(target.RecordId, target.RR, target.Type, ipaddr)
	}
	if err != nil && strings.Contains(err.Error(), `DomainRecordDuplicate`) {
		ak.DelRecord(rr, domain)
		return ak.CheckAndUpdateRecord(rr, domain, ipaddr, ipv6)
	}
	return err
}

var (
	accessKey     AccessKey
	VersionString = "MISSING build version [git hash]"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	app := cli.NewApp()
	app.Name = "aliddns"
	app.Usage = "aliyun-ddns-cli"
	app.Version = fmt.Sprintf("Git:[%s] (%s)", strings.ToUpper(VersionString), runtime.Version())
	app.Commands = []cli.Command{
		{
			Name:     "list",
			Category: "DDNS",
			Usage:    "List AliYun's DNS DomainRecords Record",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "domain, d",
					Usage: "Specific `DomainName`. like aliyun.com",
				},
			},
			Action: func(c *cli.Context) error {
				if err := appInit(c); err != nil {
					return err
				}
				// fmt.Println(c.Command.Name, "task: ", accessKey, c.String("domain"))
				if dnsRecords, err := accessKey.ListRecord(c.String("domain")); err != nil {
					fmt.Printf("%+v", err)
				} else {
					for _, v := range dnsRecords {
						fmt.Printf("%20s   %-8s %s\n", v.RR+`.`+v.DomainName, v.Type, v.Value)
					}
				}
				return nil
			},
		},
		{
			Name:     "delete",
			Category: "DDNS",
			Usage:    "Delete AliYun's DNS DomainRecords Record",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "domain, d",
					Usage: "Specific `FullDomainName`. like ddns.aliyun.com",
				},
			},
			Action: func(c *cli.Context) error {
				if err := appInit(c); err != nil {
					return err
				}
				// fmt.Println(c.Command.Name, "task: ", accessKey, c.String("domain"))
				if err := accessKey.DelRecord(splitDomain(c.String("domain"))); err != nil {
					fmt.Printf("%+v", err)
				} else {
					fmt.Println(c.String("domain"), "Deleted")
				}
				return nil
			},
		},
		{
			Name:     "update",
			Category: "DDNS",
			Usage:    "Update AliYun's DNS DomainRecords Record, Create Record if not exist",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "domain, d",
					Usage: "Specific `DomainName`. like ddns.aliyun.com",
				},
				cli.StringFlag{
					Name:  "ipaddr, i",
					Usage: "Specific `IP`. like 1.2.3.4",
				},
				cli.BoolFlag{
					Name:  "ipv6, 6",
					Usage: "update IPv6 address",
				},
			},
			Action: func(c *cli.Context) error {
				if err := appInit(c); err != nil {
					return err
				}
				fmt.Println(c.Command.Name, "task: ", accessKey, c.String("domain"), c.String("ipaddr"))
				rr, domain := splitDomain(c.String("domain"))
				if err := accessKey.CheckAndUpdateRecord(rr, domain, c.String("ipaddr"), c.Bool("ipv6")); err != nil {
					log.Printf("%+v", err)
				} else {
					log.Println(c.String("domain"), c.String("ipaddr"), ip2locCN(c.String("ipaddr")))
				}
				return nil
			},
		},
		{
			Name:     "auto-update",
			Category: "DDNS",
			Usage:    "Auto-Update AliYun's DNS DomainRecords Record, Get IP using its getip",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "domain, d",
					Usage: "Specific `DomainName`. like ddns.aliyun.com",
				},
				cli.StringFlag{
					Name:  "redo, r",
					Value: "",
					Usage: "redo Auto-Update, every N `Seconds`; Disable if N less than 10; End with [Rr] enable random delay: [N, 2N]",
				},
				cli.BoolFlag{
					Name:  "ipv6, 6",
					Usage: "update IPv6 address",
				},
			},
			Action: func(c *cli.Context) error {
				if err := appInit(c); err != nil {
					return err
				}
				// fmt.Println(c.Command.Name, "task: ", accessKey, c.String("domain"), c.Int64("redo"))
				rr, domain := splitDomain(c.String("domain"))
				redoDurtionStr := c.String("redo")
				if len(redoDurtionStr) > 0 && !regexp.MustCompile(`\d+[Rr]?$`).MatchString(redoDurtionStr) {
					return errors.New(`redo format: [0-9]+[Rr]?$`)
				}
				randomDelay := regexp.MustCompile(`\d+[Rr]$`).MatchString(redoDurtionStr)
				redoDurtion := 0
				if randomDelay {
					// Print Version if exist
					if !strings.HasPrefix(VersionString, "MISSING") {
						fmt.Fprintf(os.Stderr, "%s %s\n", strings.ToUpper(c.App.Name), c.App.Version)
					}
					redoDurtion, _ = strconv.Atoi(redoDurtionStr[:len(redoDurtionStr)-1])
				} else {
					redoDurtion, _ = strconv.Atoi(redoDurtionStr)
				}
				for {
					autoip := getIP()
					if c.Bool("ipv6") {
						autoip = getIP6()
					}
					if len(autoip) == 0 {
						log.Printf("# Err-CheckAndUpdateRecord: [%s]", "IP is empty, PLZ check network")
					} else {
						if err := accessKey.CheckAndUpdateRecord(rr, domain, autoip, c.Bool("ipv6")); err != nil {
							log.Printf("# Err-CheckAndUpdateRecord: [%+v]", err)
						} else {
							log.Println(c.String("domain"), autoip, ip2locCN(autoip))
						}
					}
					if redoDurtion < 10 {
						break // Disable if N less than 10
					}
					if randomDelay {
						time.Sleep(time.Duration(redoDurtion+rand.Intn(redoDurtion)) * time.Second)
					} else {
						time.Sleep(time.Duration(redoDurtion) * time.Second)
					}
				}
				return nil
			},
		},
		{
			Name:     "getip",
			Category: "GET-IP",
			Usage:    fmt.Sprintf("      Get IP Combine %d different Web-API", len(ipAPI)),
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "ipv6, 6",
					Usage: "IPv6",
				},
			},
			Action: func(c *cli.Context) error {
				// fmt.Println(c.Command.Name, "task: ", c.Command.Usage)
				if c.Bool("ipv6") {
					ip := getIP6()
					fmt.Println(ip)
				} else {
					ip := getIP()
					fmt.Println(ip, ip2locCN(ip))
				}
				return nil
			},
		},
		{
			Name:     "resolve",
			Category: "GET-IP",
			Usage:    fmt.Sprintf("      Get DNS-IPv4 Combine %d DNS Upstream", len(dnsUpStream)),
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "domain, d",
					Usage: "Specific `DomainName`. like ddns.aliyun.com",
				},
				cli.BoolFlag{
					Name:  "ipv6, 6",
					Usage: "IPv6",
				},
			},
			Action: func(c *cli.Context) error {
				// fmt.Println(c.Command.Name, "task: ", c.Command.Usage)
				ip := getDNS(c.String("domain"), c.Bool("ipv6"))
				if len(ip) < 1 {
					return nil
				}
				if c.Bool("ipv6") {
					fmt.Println(ip)
				} else {
					fmt.Println(ip, ip2locCN(ip))
				}
				return nil
			},
		},
	}
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "access-key-id, id",
			Usage: "AliYun's Access Key ID",
		},
		cli.StringFlag{
			Name:  "access-key-secret, secret",
			Usage: "AliYun's Access Key Secret",
		},
		cli.StringSliceFlag{
			Name:  "ipapi, api",
			Usage: "Web-API to Get IP, like: http://myip.ipip.net",
		},
	}
	app.Action = func(c *cli.Context) error {
		return appInit(c)
	}
	app.Run(os.Args)
}

func appInit(c *cli.Context) error {
	akids := []string{c.GlobalString("access-key-id"), os.Getenv("AKID"), os.Getenv("AccessKeyID")}
	akscts := []string{c.GlobalString("access-key-secret"), os.Getenv("AKSCT"), os.Getenv("AccessKeySecret")}
	sort.Sort(sort.Reverse(sort.StringSlice(akids)))
	sort.Sort(sort.Reverse(sort.StringSlice(akscts)))
	accessKey.ID = akids[0]
	accessKey.Secret = akscts[0]
	if accessKey.getClient() == nil {
		cli.ShowAppHelp(c)
		return errors.New("access-key is empty")
	}

	newIPAPI := make([]string, 0)
	for _, api := range c.GlobalStringSlice("ipapi") {
		if !regexp.MustCompile(`^https?://.*`).MatchString(api) {
			api = "http://" + api
		}
		if regexp.MustCompile(`(https?|ftp|file)://[-A-Za-z0-9+&@#/%?=~_|!:,.;]+[-A-Za-z0-9+&@#/%=~_|]`).MatchString(api) {
			newIPAPI = append(newIPAPI, api)
		}
	}
	if len(newIPAPI) > 0 {
		ipAPI = newIPAPI
	}

	return nil
}
