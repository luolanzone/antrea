if [[ "$1" == "a" || "$1" == "b" ]];then
echo "build antrea binary"
make antrea-agent antrea-controller antrea-cni antctl-linux
mv bin/antctl-linux bin/antctl
NO_PULL=1 make ubuntu
fi

if [[ "$1" == "c" || "$1" == "a" ]];then
echo "build mc controller binary"
cd multicluster;make build;cd -
NO_PULL=1 make antrea-mc-controller
fi

