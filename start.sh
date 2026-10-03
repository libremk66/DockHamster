#!/bin/sh
# 判断当前目录下是否存在名为 dockhamster-new 的二进制文件
if [ -f "./dockhamster-new" ]; then
    # 如果存在，则用它覆盖 dockhamster
    mv ./dockhamster-new ./dockhamster
    # 赋予 dockhamster 执行权限
    chmod +x ./dockhamster
fi

# 运行 dockhamster
./dockhamster