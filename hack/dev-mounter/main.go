package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/argoproj/argo-cd/v3/util/cli"
	"github.com/argoproj/argo-cd/v3/util/errors"

	// load the gcp plugin (required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	// load the oidc plugin (required to authenticate with OpenID Connect).
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	// load the azure plugin (required to authenticate with AKS clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/azure"
)

func newCommand() *cobra.Command {
	var (
		clientConfig clientcmd.ClientConfig
		configMaps   []string
	)
	command := cobra.Command{
		Run: func(_ *cobra.Command, _ []string) {
			config, err := clientConfig.ClientConfig()
			errors.CheckError(err)
			ns, _, err := clientConfig.Namespace()
			errors.CheckError(err)
			cmNameToPath := make(map[string]string)
			for _, cm := range configMaps {
				parts := strings.Split(cm, "=")
				if len(parts) != 2 {
					log.Fatal("--configmap value should be include config map name and the path separated by '='")
				}
				log.Infof("Saving %s to %s", parts[0], parts[1])
				cmNameToPath[parts[0]] = parts[1]
			}

			handledConfigMap := func(obj any) {
				cm, ok := obj.(*corev1.ConfigMap)
				if !ok {
					return
				}
				destPath, ok := cmNameToPath[cm.Name]
				if !ok {
					return
				}
				err := os.MkdirAll(destPath, os.ModePerm)
				if err != nil {
					log.Warnf("Failed to create directory: %v", err)
					return
				}
				// Remove files that do not exist in ConfigMap anymore
				err = filepath.Walk(destPath, func(path string, info os.FileInfo, err error) error {
					if info.IsDir() {
						return nil
					}
					if err != nil {
						log.Warnf("Error walking path %s: %v", path, err)
					}
					p := filepath.Base(path)
					if _, ok := cm.Data[p]; !ok {
						log.Infof("Removing file '%s'", path)
						err := os.Remove(path)
						if err != nil {
							log.Warnf("Failed to remove file %s: %v", path, err)
						}
					}
					return nil
				})
				if err != nil {
					log.Fatalf("Error: %v", err)
				}
				// Create or update files that are specified in ConfigMap
				for name, data := range cm.Data {
					p := path.Join(destPath, name)
					err := os.WriteFile(p, []byte(data), 0o644)
					if err != nil {
						log.Warnf("Failed to create file %s: %v", p, err)
					}
				}
			}

			kubeClient := kubernetes.NewForConfigOrDie(config)
			factory := informers.NewSharedInformerFactoryWithOptions(kubeClient, 1*time.Minute, informers.WithNamespace(ns))
			informer := factory.Core().V1().ConfigMaps().Informer()
			_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: handledConfigMap,
				UpdateFunc: func(_, newObj any) {
					handledConfigMap(newObj)
				},
			})
			if err != nil {
				log.Error(err)
			}
			informer.Run(context.Background().Done())
		},
	}
	clientConfig = cli.AddKubectlFlagsToCmd(&command)
	command.Flags().StringArrayVar(&configMaps, "configmap", nil, "Config Map name and corresponding path. E.g. argocd-ssh-known-hosts-cm=/tmp/argocd/ssh")
	return &command
}

func main() {
	if err := newCommand().Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
