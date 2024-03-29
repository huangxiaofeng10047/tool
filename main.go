package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"text/template"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/spf13/pflag"
)

var (
	content    = pflag.StringP("content", "", "", "原始镜像，格式为： {\" hub-mirror\":[\"hub-mirror.cn-hangzhou.aliyuncs.com\")")
	maxContent = pflag.IntP("maxContent", "", 10, "原始镜像个数限制")
	username   = pflag.StringP("username", "", "", "阿里云账号")
	password   = pflag.StringP("password", "", "", "阿里云密码")
	outputPath = pflag.StringP("outputPath", "", "output.sh", "结果输出文件路径")
)

func main() {
	pflag.Parse()
	fmt.Println("验证原始镜像内容")
	var hubMirrors struct {
		Content []string `json:"hub-mirror"`
	}
	err := json.Unmarshal([]byte(*content), &hubMirrors)
	if err != nil {
		panic(err)
	}
	if len(hubMirrors.Content) > *maxContent {
		panic("原始镜像个数超过限制")
	}
	fmt.Println("%+v\n", hubMirrors)
	fmt.Println("连接docker")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}
	fmt.Println("验证 Docker 用户名和密码")

	if *username == "" || *password == "" {
		panic("用户名和密码不能为空")
	}

	autoConfig := registry.AuthConfig{
		Username: *username,
		Password: *password,
	}

	encodedJSON, err := json.Marshal(autoConfig)
	if err != nil {
		panic(err)
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)
	_, err = cli.RegistryLogin(context.Background(), autoConfig)
	if err != nil {
		panic(err)
	}
	fmt.Println("开始转换镜像")

	output := make([]struct {
		Source string
		Target string
	}, 0)
	wg := sync.WaitGroup{}
	for _, source := range hubMirrors.Content {
		if source == "" {
			continue
		}
		target := *username + "/" + strings.ReplaceAll(source, "/", ".")
		wg.Add(1)
		go func(source, target string) {
			defer wg.Done()
			fmt.Printf("开始转换镜像 %s -> %s\n", source, "=>", target)
			ctx := context.Background()

			// pull image
			pullOut, err := cli.ImagePull(ctx, source, types.ImagePullOptions{})
			if err != nil {
				panic(err)
			}
			defer pullOut.Close()
			io.Copy(os.Stdout, pullOut)
			//重新打标签
			err = cli.ImageTag(ctx, source, target)
			if err != nil {
				panic(err)
			}
			// push image
			pushOut, err := cli.ImagePush(ctx, target, types.ImagePushOptions{RegistryAuth: authStr})
			if err != nil {
				panic(err)
			}
			defer pushOut.Close()
			io.Copy(os.Stdout, pushOut)
			output = append(output, struct {
				Source string
				Target string
			}{Source: source, Target: target})
			fmt.Printf("转换镜像 %s -> %s 完成\n", source, target)
		}(source, target)
	}
	wg.Wait()
	if len(output) == 0 {
		panic("没有镜像需要转换")
	}

	tmpl, err := template.New("pull_images").Parse(`{{- range . -}}
    docker pull {{.Target}}
    docker tag {{.Target}} {{.Source}} 
    {{- end -}}`)
	if err != nil {
		panic(err)
	}
	outputFile, err := os.Create(*outputPath)
	if err != nil {
		panic(err)
	}
	defer outputFile.Close()
	err = tmpl.Execute(outputFile, output)
	if err != nil {
		panic(err)
	}
	fmt.Println(output)
}
