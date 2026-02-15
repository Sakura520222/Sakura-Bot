#!/bin/bash

# Sakura Channel Summary Assistant - Linux 一键部署脚本
# 功能：自动创建虚拟环境、安装依赖并启动应用

set -e  # 遇到错误时退出

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印带颜色的消息
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 Python 是否安装
check_python() {
    print_info "检查 Python 环境..."
    
    if ! command -v python3 &> /dev/null; then
        print_error "未找到 Python3，请先安装 Python 3.8 或更高版本"
        exit 1
    fi
    
    PYTHON_VERSION=$(python3 --version | cut -d' ' -f2 | cut -d'.' -f1,2)
    print_success "找到 Python 版本: $PYTHON_VERSION"
    
    # 检查 Python 版本是否 >= 3.8
    if ! python3 -c "import sys; exit(0 if sys.version_info >= (3, 8) else 1)" 2>/dev/null; then
        print_error "需要 Python 3.8 或更高版本"
        exit 1
    fi
}

# 创建或激活虚拟环境
setup_venv() {
    VENV_DIR="venv"
    
    if [ -d "$VENV_DIR" ]; then
        print_info "检测到已存在的虚拟环境"
        
        # 询问是否重新创建
        read -p "是否要重新创建虚拟环境？这将删除现有环境 [y/N]: " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            print_warning "删除旧的虚拟环境..."
            rm -rf "$VENV_DIR"
            create_venv
        fi
    else
        create_venv
    fi
    
    # 激活虚拟环境
    print_info "激活虚拟环境..."
    source "$VENV_DIR/bin/activate"
    print_success "虚拟环境已激活"
}

# 创建虚拟环境
create_venv() {
    print_info "创建 Python 虚拟环境..."
    python3 -m venv "$VENV_DIR"
    print_success "虚拟环境创建完成"
}

# 安装依赖
install_dependencies() {
    print_info "检查并安装依赖包..."
    
    if [ -f "requirements.txt" ]; then
        print_info "从 requirements.txt 安装依赖..."
        pip install --upgrade pip -q
        pip install -r requirements.txt
        print_success "依赖包安装完成"
    else
        print_error "未找到 requirements.txt 文件"
        exit 1
    fi
}

# 检查 PM2
check_pm2() {
    print_info "检查 PM2 进程管理器..."
    
    if ! command -v pm2 &> /dev/null; then
        print_warning "未找到 PM2"
        
        # 检查 Node.js 和 npm
        if ! command -v node &> /dev/null || ! command -v npm &> /dev/null; then
            print_error "PM2 需要 Node.js 和 npm，但未检测到"
            print_info "请先安装 Node.js:"
            print_info "  curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash -"
            print_info "  sudo apt-get install -y nodejs"
            exit 1
        fi
        
        print_info "检测到 Node.js，正在安装 PM2..."
        npm install -g pm2
        
        if [ $? -eq 0 ]; then
            print_success "PM2 安装成功"
        else
            print_error "PM2 安装失败"
            exit 1
        fi
    else
        print_success "PM2 已安装"
    fi
}

# 检查配置文件
check_config() {
    print_info "检查配置文件..."
    
    if [ ! -f "data/.env" ]; then
        print_warning "未找到 data/.env 配置文件"
        if [ -f "data/.env.example" ]; then
            print_info "从 .env.example 创建配置文件..."
            cp data/.env.example data/.env
            print_warning "请编辑 data/.env 文件并填入必要的配置信息"
            print_warning "特别是 API 密钥和 Telegram 配置"
            read -p "是否现在打开编辑配置文件？ [Y/n]: " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Nn]$ ]]; then
                ${EDITOR:-nano} data/.env
            fi
        else
            print_error "未找到配置文件模板"
            exit 1
        fi
    fi
    
    print_success "配置文件检查完成"
}

# 更新代码
update_code() {
    print_info "检查是否为 Git 仓库..."
    
    # 检查是否在 git 仓库中
    if ! git rev-parse --git-dir > /dev/null 2>&1; then
        print_warning "当前目录不是 Git 仓库，跳过更新"
        return 0
    fi
    
    print_success "检测到 Git 仓库"
    
    # 显示当前 commit 信息
    CURRENT_COMMIT=$(git rev-parse --short HEAD)
    CURRENT_BRANCH=$(git branch --show-current)
    print_info "当前分支: $CURRENT_BRANCH"
    print_info "当前版本: $CURRENT_COMMIT"
    
    # 警告用户
    echo ""
    print_warning "⚠️  警告：此操作将放弃所有已跟踪文件的本地更改！"
    print_warning "受影响的文件："
    print_warning "  - 所有已跟踪的代码文件修改"
    print_warning "  - 已提交到 Git 的配置文件更改"
    print_info "ℹ️  未跟踪的文件（如 data/.env）将被保留"
    echo ""
    
    # 询问是否继续
    read -p "是否继续从远程拉取最新代码？ [y/N]: " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        print_info "跳过代码更新"
        return 0
    fi
    
    # 获取远程分支名
    print_info "获取远程更新..."
    git fetch origin
    
    # 检测远程主分支名称（main 或 master）
    if git show-ref --verify --quiet refs/remotes/origin/main; then
        REMOTE_BRANCH="origin/main"
    elif git show-ref --verify --quiet refs/remotes/origin/master; then
        REMOTE_BRANCH="origin/master"
    else
        print_error "未找到远程 main 或 master 分支"
        return 1
    fi
    
    print_info "远程分支: $REMOTE_BRANCH"
    
    # 重置到远程最新代码
    print_warning "正在重置到远程最新代码（放弃本地更改）..."
    git reset --hard "$REMOTE_BRANCH"
    
    if [ $? -eq 0 ]; then
        # 获取最新 commit 信息
        NEW_COMMIT=$(git rev-parse --short HEAD)
        NEW_COMMIT_MSG=$(git log -1 --pretty=format:"%s")
        
        print_success "代码更新成功！"
        print_info "新版本: $NEW_COMMIT"
        print_info "更新内容: $NEW_COMMIT_MSG"
        
        # 赋予自身执行权限
        print_info "赋予脚本执行权限..."
        chmod +x "$0"
        if [ $? -eq 0 ]; then
            print_success "脚本执行权限已设置"
        else
            print_warning "设置执行权限失败，请手动执行: chmod +x start.sh"
        fi
    else
        print_error "代码更新失败"
        return 1
    fi
}

# 使用 PM2 启动应用
start_with_pm2() {
    print_info "使用 PM2 启动应用..."
    
    # 检查是否已有同名进程在运行
    if pm2 list | grep -q "sakura-bot"; then
        print_warning "检测到 sakura-bot 进程已在运行"
        read -p "是否重启？ [Y/n]: " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Nn]$ ]]; then
            pm2 restart sakura-bot
            print_success "应用已重启"
        else
            print_info "保持当前进程运行"
        fi
    else
        # 启动新进程
        pm2 start main.py --name sakura-bot --interpreter python3
        print_success "应用已启动"
    fi
    
    print_info "显示 PM2 进程状态..."
    pm2 list
    
    print_info "查看日志命令: pm2 logs sakura-bot"
    print_info "停止应用命令: pm2 stop sakura-bot"
    print_info "重启应用命令: pm2 restart sakura-bot"
    print_info "删除应用命令: pm2 delete sakura-bot"
    print_info "保存 PM2 配置: pm2 save"
    print_info "设置 PM2 开机自启: pm2 startup"
}

# 主函数
main() {
    echo "=========================================="
    echo "  Sakura Channel Summary Assistant"
    echo "  Linux 一键部署脚本"
    echo "=========================================="
    echo ""
    
    # 更新代码（可选）
    update_code
    echo ""
    
    # 检查 Python
    check_python
    echo ""
    
    # 设置虚拟环境
    setup_venv
    echo ""
    
    # 安装依赖
    install_dependencies
    echo ""
    
    # 检查配置
    check_config
    echo ""
    
    # 检查并安装 PM2
    check_pm2
    echo ""
    
    print_success "环境配置完成！"
    print_info "使用 PM2 启动应用程序..."
    echo ""
    
    # 使用 PM2 启动应用
    start_with_pm2
}

# 运行主函数
main
