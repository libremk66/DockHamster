package utiles

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/libremk66/DockHamster/internal/svc"
	MyType "github.com/libremk66/DockHamster/internal/types"
)

// imageParentCacheTTL 父镜像集合的缓存时长。
// 父镜像关系只在镜像构建/删除时变化，而镜像列表会被前端高频轮询，
// 缓存可避免每次列表都逐个 inspect（悬空镜像多时开销显著）。
const imageParentCacheTTL = 30 * time.Second

var (
	imageParentCacheMu      sync.RWMutex
	imageParentCache        map[string]bool
	imageParentCacheExpires time.Time
)

// imageParentScan 全量扫描镜像、收集父镜像 ID 集合；抽为变量便于测试替换。
var imageParentScan = scanImageParentSet

func GetImagesList(ctx *svc.ServiceContext) ([]MyType.Image, error) {
	var imagesList []MyType.Image
	dockerImages, err := ctx.DockerClient.ImageList(context.Background(), image.ListOptions{})
	if err != nil {
		log.Fatalf("Unable to fetch docker images: %s", err)
	}

	for _, img := range dockerImages {
		i := MyType.Image{
			Summary:    img,
			ImageName:  "",
			ImageTag:   "",
			InUsed:     false,
			SizeFormat: "",
		}
		imagesList = append(imagesList, i)
	}
	//看不明白就不要看了，这内存反复地申请，如果你看明白了 给这改成指针吧，啥？我为啥不直接写指针，我懒癌犯了就这样，欢迎pr
	imagesList, err = checkImageInUsed(ctx, splitImageNameAndTag(calculateImageSize(imagesList)))
	if err != nil {
		return imagesList, err
	}
	markChildImages(ctx, imagesList)
	return imagesList, nil
}

// markChildImages 标记被其他镜像作为基础镜像引用的镜像（本地构建产物的父镜像）。
// 此类镜像即使无容器使用也无法删除（daemon 拒绝：image has dependent child images）。
func markChildImages(ctx *svc.ServiceContext, imagesList []MyType.Image) {
	parentSet := imageParentSet(ctx)
	for i := range imagesList {
		if parentSet[imagesList[i].ID] {
			imagesList[i].HasChildren = true
		}
	}
}

// imageParentSet 收集所有镜像的父镜像 ID 集合（带 TTL 缓存）。
func imageParentSet(ctx *svc.ServiceContext) map[string]bool {
	imageParentCacheMu.RLock()
	if imageParentCache != nil && time.Now().Before(imageParentCacheExpires) {
		cached := imageParentCache
		imageParentCacheMu.RUnlock()
		return cached
	}
	imageParentCacheMu.RUnlock()

	parents := imageParentScan(ctx)

	imageParentCacheMu.Lock()
	imageParentCache = parents
	imageParentCacheExpires = time.Now().Add(imageParentCacheTTL)
	imageParentCacheMu.Unlock()
	return parents
}

// scanImageParentSet 全量扫描镜像并收集父镜像 ID。
func scanImageParentSet(ctx *svc.ServiceContext) map[string]bool {
	parents := make(map[string]bool)
	list, err := ctx.DockerClient.ImageList(context.Background(), image.ListOptions{})
	if err != nil {
		return parents
	}
	for _, img := range list {
		ins, _, err := ctx.DockerClient.ImageInspectWithRaw(context.Background(), img.ID)
		if err != nil || ins.Parent == "" {
			continue
		}
		parents[ins.Parent] = true
	}
	return parents
}

// InvalidateImageParentCache 清除父镜像集合缓存。
// 镜像构建/删除后调用，避免缓存窗口内列表展示过期的依赖状态。
func InvalidateImageParentCache() {
	imageParentCacheMu.Lock()
	imageParentCache = nil
	imageParentCacheExpires = time.Time{}
	imageParentCacheMu.Unlock()
}

func splitImageNameAndTag(imagesList []MyType.Image) []MyType.Image {
	for i, imageInfo := range imagesList {
		imagesList[i].Tags = imageInfo.RepoTags
		if len(imageInfo.RepoTags) != 0 {
			imagesList[i].ImageName = strings.Split(imageInfo.RepoTags[0], ":")[0]
			imagesList[i].ImageTag = strings.Split(imageInfo.RepoTags[0], ":")[1]
		} else if len(imageInfo.RepoDigests) != 0 {
			imagesList[i].ImageName = strings.Split(imageInfo.RepoDigests[0], "@")[0]
			imagesList[i].ImageTag = "None"
		} else {
			imagesList[i].ImageName = "None"
			imagesList[i].ImageTag = "None"
		}
	}
	return imagesList
}
func checkImageInUsed(svc *svc.ServiceContext, imageList []MyType.Image) ([]MyType.Image, error) {
	list, err := GetContainerList(svc)
	if err != nil {
		return imageList, err
	}
	// 这里可以用mapreduce 我懒等pr
	for _, v := range list {
		for i, imageInfo := range imageList {
			if v.ImageID == imageInfo.ID {
				imageList[i].InUsed = true
				break
			}
		}
	}
	return imageList, nil
}
func calculateImageSize(imagesList []MyType.Image) []MyType.Image {
	for i := range imagesList {
		if imagesList[i].Size >= 1024*1024*1024 {
			imagesList[i].SizeFormat = // Convert size to gigabytes
				fmt.Sprintf("%d Gb", imagesList[i].Size/1024/1024/1024)
		} else {
			imagesList[i].SizeFormat = // Convert size to megabytes
				fmt.Sprintf("%d Mb", imagesList[i].Size/1024/1024)
		}
	}
	return imagesList
}
