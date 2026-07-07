#!/bin/bash
# DiffFlow Linux 部署脚本

set -e

APP_NAME="diffflow-server"
BINARY_NAME="diffflow-server-linux"
INSTALL_DIR="/opt/diffflow"
SERVICE_PATH="/etc/systemd/system/diffflow.service"

echo "开始部署 DiffFlow 服务器..."

# 1. 创建目录
sudo mkdir -p $INSTALL_DIR

# 2. 移动二进制文件和配置文件 (假设当前目录下有该文件)
if [ -f "./$BINARY_NAME" ]; then
    sudo cp ./$BINARY_NAME $INSTALL_DIR/$APP_NAME
    sudo chmod +x $INSTALL_DIR/$APP_NAME
else
    echo "错误: 找不到 $BINARY_NAME，请确保已上传该文件。"
    exit 1
fi

sudo mkdir -p $INSTALL_DIR/configs $INSTALL_DIR/data/files
if [ -f "./configs/default.toml" ]; then
    sudo cp ./configs/default.toml $INSTALL_DIR/configs/default.toml
else
    echo "错误: 找不到 configs/default.toml，请确保已上传配置文件。"
    exit 1
fi

# 3. 创建 Systemd 服务文件
sudo bash -c "cat > $SERVICE_PATH" <<EOF
[Unit]
Description=DiffFlow Real-time Sync Server
After=network.target

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$APP_NAME -config $INSTALL_DIR/configs/default.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# 4. 重新加载并启动
sudo systemctl daemon-reload
sudo systemctl enable diffflow
sudo systemctl restart diffflow

echo "部署完成！服务已启动并设置为开机自启。"
echo "您可以通过 'journalctl -u diffflow -f' 查看实时日志。"
