/**
 * Sakura频道总结助手 - 全局JavaScript
 */

// ==========================================
// 主题管理
// ==========================================

/**
 * 切换主题
 */
function toggleTheme() {
    const html = document.documentElement;
    const currentTheme = html.getAttribute('data-bs-theme');
    const newTheme = currentTheme === 'light' ? 'dark' : 'light';
    
    html.setAttribute('data-bs-theme', newTheme);
    localStorage.setItem('theme', newTheme);
    
    updateThemeUI(newTheme);
    showToast(`已切换到${newTheme === 'dark' ? '暗色' : '亮色'}主题`, 'success');
}

/**
 * 加载保存的主题
 */
function loadSavedTheme() {
    const savedTheme = localStorage.getItem('theme') || 'light';
    document.documentElement.setAttribute('data-bs-theme', savedTheme);
    updateThemeUI(savedTheme);
}

/**
 * 更新主题UI
 */
function updateThemeUI(theme) {
    const icon = document.getElementById('themeIcon');
    const text = document.getElementById('themeText');
    
    if (icon) {
        icon.className = theme === 'dark' ? 'bi bi-sun' : 'bi bi-moon-stars';
    }
    
    if (text) {
        text.textContent = theme === 'dark' ? '亮色' : '暗色';
    }
}

// ==========================================
// Toast通知系统
// ==========================================

/**
 * 显示Toast通知
 * @param {string} message - 通知消息
 * @param {string} type - 通知类型 (success, danger, warning, info)
 * @param {number} delay - 显示时长（毫秒）
 */
function showToast(message, type = 'info', delay = 3000) {
    const toastContainer = document.getElementById('toastContainer');
    if (!toastContainer) {
        return;
    }
    
    // 生成唯一ID
    const toastId = 'toast-' + Date.now() + '-' + Math.random().toString(36).substr(2, 9);
    
    // 类型映射
    const typeMap = {
        'success': { bgClass: 'text-bg-success', icon: 'bi-check-circle-fill' },
        'danger': { bgClass: 'text-bg-danger', icon: 'bi-exclamation-triangle-fill' },
        'warning': { bgClass: 'text-bg-warning', icon: 'bi-exclamation-circle-fill' },
        'info': { bgClass: 'text-bg-info', icon: 'bi-info-circle-fill' }
    };
    
    const typeInfo = typeMap[type] || typeMap['info'];
    
    // 创建Toast元素
    const toast = document.createElement('div');
    toast.id = toastId;
    toast.className = `toast align-items-center ${typeInfo.bgClass} border-0`;
    toast.setAttribute('role', 'alert');
    toast.setAttribute('aria-live', 'assertive');
    toast.setAttribute('aria-atomic', 'true');
    
    toast.innerHTML = `
        <div class="d-flex">
            <div class="toast-body">
                <i class="bi ${typeInfo.icon} me-2"></i>
                ${escapeHtml(message)}
            </div>
            <button type="button" class="btn-close btn-close-white me-2 m-auto" 
                    data-bs-dismiss="toast" aria-label="关闭"></button>
        </div>
    `;
    
    toastContainer.appendChild(toast);
    
    // 显示Toast
    const bsToast = new bootstrap.Toast(toast, { delay: delay });
    bsToast.show();
    
    // 自动移除
    toast.addEventListener('hidden.bs.toast', function () {
        toast.remove();
    });
}

/**
 * 显示成功消息
 */
function showSuccess(message, delay = 3000) {
    showToast(message, 'success', delay);
}

/**
 * 显示错误消息
 */
function showError(message, delay = 3000) {
    showToast(message, 'danger', delay);
}

/**
 * 显示警告消息
 */
function showWarning(message, delay = 3000) {
    showToast(message, 'warning', delay);
}

/**
 * 显示信息消息
 */
function showInfo(message, delay = 3000) {
    showToast(message, 'info', delay);
}

// ==========================================
// 全局加载器
// ==========================================

/**
 * 显示全局加载遮罩
 */
function showGlobalLoader() {
    const loader = document.getElementById('globalLoader');
    if (loader) {
        loader.classList.remove('d-none');
        loader.classList.add('d-flex');
    }
}

/**
 * 隐藏全局加载遮罩
 */
function hideGlobalLoader() {
    const loader = document.getElementById('globalLoader');
    if (loader) {
        loader.classList.add('d-none');
        loader.classList.remove('d-flex');
    }
}

// ==========================================
// 工具函数
// ==========================================

/**
 * HTML转义
 * @param {string} text - 要转义的文本
 * @returns {string} 转义后的文本
 */
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

/**
 * 复制到剪贴板
 * @param {string} text - 要复制的文本
 * @returns {Promise<boolean>} 是否成功
 */
async function copyToClipboard(text) {
    try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
            await navigator.clipboard.writeText(text);
            showToast('已复制到剪贴板', 'success');
            return true;
        } else {
            // 降级方案
            const textarea = document.createElement('textarea');
            textarea.value = text;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            
            try {
                document.execCommand('copy');
                showToast('已复制到剪贴板', 'success');
                return true;
            } catch (err) {
                console.error('复制失败:', err);
                showToast('复制失败', 'danger');
                return false;
            } finally {
                document.body.removeChild(textarea);
            }
        }
    } catch (err) {
        console.error('复制失败:', err);
        showToast('复制失败', 'danger');
        return false;
    }
}

/**
 * 格式化日期
 * @param {Date|string} date - 日期对象或字符串
 * @param {string} format - 格式化字符串
 * @returns {string} 格式化后的日期
 */
function formatDate(date, format = 'YYYY-MM-DD HH:mm:ss') {
    const d = new Date(date);
    
    const year = d.getFullYear();
    const month = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    const hours = String(d.getHours()).padStart(2, '0');
    const minutes = String(d.getMinutes()).padStart(2, '0');
    const seconds = String(d.getSeconds()).padStart(2, '0');
    
    return format
        .replace('YYYY', year)
        .replace('MM', month)
        .replace('DD', day)
        .replace('HH', hours)
        .replace('mm', minutes)
        .replace('ss', seconds);
}

/**
 * 格式化相对时间
 * @param {Date|string} date - 日期
 * @returns {string} 相对时间字符串
 */
function formatRelativeTime(date) {
    const now = new Date();
    const d = new Date(date);
    const diff = now - d;
    
    const seconds = Math.floor(diff / 1000);
    const minutes = Math.floor(seconds / 60);
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);
    
    if (seconds < 60) {
        return '刚刚';
    } else if (minutes < 60) {
        return `${minutes}分钟前`;
    } else if (hours < 24) {
        return `${hours}小时前`;
    } else if (days < 7) {
        return `${days}天前`;
    } else {
        return formatDate(date, 'YYYY-MM-DD');
    }
}

/**
 * 防抖函数
 * @param {Function} func - 要防抖的函数
 * @param {number} wait - 等待时间（毫秒）
 * @returns {Function} 防抖后的函数
 */
function debounce(func, wait = 300) {
    let timeout;
    return function executedFunction(...args) {
        const later = () => {
            clearTimeout(timeout);
            func(...args);
        };
        clearTimeout(timeout);
        timeout = setTimeout(later, wait);
    };
}

/**
 * 节流函数
 * @param {Function} func - 要节流的函数
 * @param {number} limit - 时间限制（毫秒）
 * @returns {Function} 节流后的函数
 */
function throttle(func, limit = 300) {
    let inThrottle;
    return function(...args) {
        if (!inThrottle) {
            func.apply(this, args);
            inThrottle = true;
            setTimeout(() => inThrottle = false, limit);
        }
    };
}

// ==========================================
// API请求封装
// ==========================================

/**
 * 发送GET请求
 * @param {string} url - 请求URL
 * @param {Object} params - 查询参数
 * @returns {Promise<Object>} 响应数据
 */
async function apiGet(url, params = {}) {
    const queryString = new URLSearchParams(params).toString();
    const fullUrl = queryString ? `${url}?${queryString}` : url;
    
    const response = await fetch(fullUrl);
    return await response.json();
}

/**
 * 发送POST请求
 * @param {string} url - 请求URL
 * @param {Object|FormData} data - 请求数据
 * @returns {Promise<Object>} 响应数据
 */
async function apiPost(url, data = {}) {
    const isFormData = data instanceof FormData;
    const response = await fetch(url, {
        method: 'POST',
        headers: isFormData ? {} : {
            'Content-Type': 'application/json'
        },
        body: isFormData ? data : JSON.stringify(data)
    });
    return await response.json();
}

/**
 * 发送PUT请求
 * @param {string} url - 请求URL
 * @param {Object} data - 请求数据
 * @returns {Promise<Object>} 响应数据
 */
async function apiPut(url, data = {}) {
    const response = await fetch(url, {
        method: 'PUT',
        headers: {
            'Content-Type': 'application/json'
        },
        body: JSON.stringify(data)
    });
    return await response.json();
}

/**
 * 发送DELETE请求
 * @param {string} url - 请求URL
 * @returns {Promise<Object>} 响应数据
 */
async function apiDelete(url) {
    const response = await fetch(url, {
        method: 'DELETE'
    });
    return await response.json();
}

// ==========================================
// 确认对话框
// ==========================================

/**
 * 显示确认对话框
 * @param {string} message - 确认消息
 * @param {string} title - 对话框标题
 * @returns {Promise<boolean>} 是否确认
 */
async function confirmDialog(message, title = '确认操作') {
    return new Promise((resolve) => {
        if (confirm(message)) {
            resolve(true);
        } else {
            resolve(false);
        }
    });
}

// ==========================================
// 键盘快捷键
// ==========================================

/**
 * 注册键盘快捷键
 */
function registerKeyboardShortcuts() {
    document.addEventListener('keydown', function(e) {
        // Ctrl/Cmd + K: 搜索
        if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
            e.preventDefault();
            const searchInput = document.querySelector('input[type="search"], input[placeholder*="搜索"]');
            if (searchInput) {
                searchInput.focus();
            }
        }
        
        // Ctrl/Cmd + R: 刷新（禁用默认刷新）
        if ((e.ctrlKey || e.metaKey) && e.key === 'r') {
            e.preventDefault();
            location.reload();
        }
        
        // ESC: 关闭模态框
        if (e.key === 'Escape') {
            const activeModal = document.querySelector('.modal.show');
            if (activeModal) {
                const modalInstance = bootstrap.Modal.getInstance(activeModal);
                if (modalInstance) {
                    modalInstance.hide();
                }
            }
        }
    });
}

// ==========================================
// 实时刷新
// ==========================================

/**
 * 自动刷新数据
 * @param {Function} refreshFn - 刷新函数
 * @param {number} interval - 刷新间隔（毫秒）
 * @returns {Function} 停止刷新的函数
 */
function startAutoRefresh(refreshFn, interval = 30000) {
    const refreshId = setInterval(refreshFn, interval);
    
    // 返回停止函数
    return function stopAutoRefresh() {
        clearInterval(refreshId);
    };
}

// ==========================================
// 表格工具
// ==========================================

/**
 * 导出表格为CSV
 * @param {string} tableId - 表格ID
 * @param {string} filename - 文件名
 */
function exportTableToCSV(tableId, filename) {
    const table = document.getElementById(tableId);
    if (!table) {
        showToast('表格不存在', 'danger');
        return;
    }
    
    const rows = table.querySelectorAll('tr');
    let csv = [];
    
    for (const row of rows) {
        const cols = row.querySelectorAll('td, th');
        const rowData = [];
        for (const col of cols) {
            rowData.push('"' + col.innerText.replace(/"/g, '""') + '"');
        }
        csv.push(rowData.join(','));
    }
    
    const csvContent = csv.join('\n');
    downloadFile(csvContent, filename, 'text/csv');
}

/**
 * 导出表格为Excel
 * @param {string} tableId - 表格ID
 * @param {string} filename - 文件名
 */
function exportTableToExcel(tableId, filename) {
    const table = document.getElementById(tableId);
    if (!table) {
        showToast('表格不存在', 'danger');
        return;
    }
    
    const html = table.outerHTML;
    const blob = new Blob([html], { type: 'application/vnd.ms-excel' });
    downloadFile(blob, filename, 'application/vnd.ms-excel');
}

/**
 * 下载文件
 * @param {string|Blob} content - 文件内容或Blob对象
 * @param {string} filename - 文件名
 * @param {string} mimeType - MIME类型
 */
function downloadFile(content, filename, mimeType = 'text/plain') {
    const blob = content instanceof Blob ? content : new Blob([content], { type: mimeType });
    const url = URL.createObjectURL(blob);
    
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    
    showToast(`文件已下载: ${filename}`, 'success');
}

// ==========================================
// 初始化
// ==========================================

/**
 * 页面加载完成后初始化
 */
document.addEventListener('DOMContentLoaded', function() {
    // 加载保存的主题
    loadSavedTheme();
    
    // 注册键盘快捷键
    registerKeyboardShortcuts();
    
    // 初始化所有工具提示
    const tooltipTriggerList = [].slice.call(document.querySelectorAll('[data-bs-toggle="tooltip"]'));
    tooltipTriggerList.map(function (tooltipTriggerEl) {
        return new bootstrap.Tooltip(tooltipTriggerEl);
    });
    
    // 初始化所有下拉菜单
    const dropdownElementList = [].slice.call(document.querySelectorAll('[data-bs-toggle="dropdown"]'));
    dropdownElementList.map(function (dropdownToggleEl) {
        return new bootstrap.Dropdown(dropdownToggleEl);
    });
    
    // 页面可见性变化时刷新（可选）
    document.addEventListener('visibilitychange', function() {
        if (!document.hidden) {
            // 页面重新可见时可以触发刷新
            // 具体实现由各页面自己决定
        }
    });
});

// ==========================================
// 图表工具
// ==========================================

/**
 * 创建图表
 * @param {string} canvasId - 画布ID
 * @param {string} type - 图表类型
 * @param {Object} data - 图表数据
 * @param {Object} options - 图表选项
 * @returns {Chart} 图表实例
 */
function createChart(canvasId, type, data, options = {}) {
    const canvas = document.getElementById(canvasId);
    if (!canvas) {
        console.error('画布不存在:', canvasId);
        return null;
    }
    
    const ctx = canvas.getContext('2d');
    
    // 默认选项
    const defaultOptions = {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
            legend: {
                position: 'top',
                labels: {
                    font: {
                        size: 12
                    }
                }
            }
        }
    };
    
    // 合并选项
    const mergedOptions = { ...defaultOptions, ...options };
    
    return new Chart(ctx, {
        type: type,
        data: data,
        options: mergedOptions
    });
}

// ==========================================
// 验证工具
// ==========================================

/**
 * 验证表单
 * @param {HTMLFormElement} form - 表单元素
 * @returns {boolean} 是否有效
 */
function validateForm(form) {
    if (!form.checkValidity()) {
        form.reportValidity();
        return false;
    }
    return true;
}

// 导出全局函数到window对象（为了兼容旧代码）
window.toggleTheme = toggleTheme;
window.showToast = showToast;
window.showSuccess = showSuccess;
window.showError = showError;
window.showWarning = showWarning;
window.showInfo = showInfo;
window.copyToClipboard = copyToClipboard;
window.showGlobalLoader = showGlobalLoader;
window.hideGlobalLoader = hideGlobalLoader;
