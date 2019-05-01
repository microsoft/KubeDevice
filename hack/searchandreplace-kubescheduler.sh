find . -name '*.go' -exec sed -i 's?k8s.io/kubernetes/plugin/cmd/kube-scheduler?github.com/KubeDevice/kube-scheduler/cmd?g' {} +
