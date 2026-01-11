/**
 * 仪表板专用JavaScript
 */

// 全局变量
let summaryTrendChart = null;
let errorStatsChart = null;
let taskHistoryForChart = [];

// 缓存配置
const CACHE_KEY_TASKS = 'taskHistoryCache';
const CACHE_KEY_TIMESTAMP = 'taskHistoryCacheTimestamp';
const CACHE_DURATION = 30 * 60 * 1000; // 30分钟

/**
 * 页面初始化
 */
document.addEventListener('DOMContentLoaded', function() {
    console.log('仪表板页面初始化...');
    
    // 加载任务历史数据（带缓存）
    loadTaskHistoryWithCache();
    
    // 初始化错误统计图表
    initErrorStatsChart();
    
    // 加载机器人状态
    refreshBotStatus();
    
    // 加载最近活动
    loadRecentActivity();
    
    // 每30秒自动刷新数据
    setInterval(refreshDashboard, 30000);
});

/**
 * 刷新仪表板数据
 */
function refreshDashboard() {
    console.log('刷新仪表板数据...');
    refreshBotStatus();
    loadRecentActivity();
    loadTaskHistoryWithCache();
    
    const lastUpdateEl = document.getElementById('lastUpdate');
    if (lastUpdateEl) {
        lastUpdateEl.textContent = formatRelativeTime(new Date());
    }
}

/**
 * 加载任务历史（带localStorage缓存支持）
 */
async function loadTaskHistoryWithCache() {
    try {
        // 尝试从localStorage加载缓存数据
        const cachedData = localStorage.getItem(CACHE_KEY_TASKS);
        const cacheTimestamp = localStorage.getItem(CACHE_KEY_TIMESTAMP);
        
        // 检查缓存是否有效（30分钟内）
        const now = Date.now();
        let useCache = false;
        
        if (cachedData && cacheTimestamp) {
            const timestamp = parseInt(cacheTimestamp);
            if (!isNaN(timestamp) && (now - timestamp < CACHE_DURATION)) {
                try {
                    taskHistoryForChart = JSON.parse(cachedData);
                    useCache = true;
                    console.log('使用缓存的任务历史数据，共', taskHistoryForChart.length, '条记录');
                } catch (e) {
                    console.warn('解析缓存数据失败，将重新加载', e);
                }
            }
        }
        
        if (!useCache) {
            // 缓存过期或不存在，从API获取数据
            const tasks = await apiGet('/api/get_recent_tasks', { limit: 200 });
            taskHistoryForChart = tasks || [];
            
            // 保存到缓存
            localStorage.setItem(CACHE_KEY_TASKS, JSON.stringify(taskHistoryForChart));
            localStorage.setItem(CACHE_KEY_TIMESTAMP, now.toString());
            console.log('任务历史数据已缓存，共', taskHistoryForChart.length, '条记录');
        } else {
            console.log('使用缓存的任务历史数据，共', taskHistoryForChart.length, '条记录');
        }
        
        initSummaryTrendChart();
    } catch (error) {
        console.error('加载任务历史失败:', error);
        taskHistoryForChart = [];
        initSummaryTrendChart();
    }
}

/**
 * 更新任务历史缓存（当有新任务时调用）
 */
function updateTaskHistoryCache(newTasks) {
    if (!Array.isArray(newTasks)) {
        return;
    }
    
    // 合并现有数据
    const existingTasks = taskHistoryForChart || [];
    const mergedTasks = [...newTasks, ...existingTasks];
    
    // 去重（根据timestamp）
    const uniqueTasks = [];
    const seenTimestamps = new Set();
    
    for (const task of mergedTasks) {
        if (!seenTimestamps.has(task.timestamp)) {
            seenTimestamps.add(task.timestamp);
            uniqueTasks.push(task);
        }
    }
    
    // 只保留最近200条
    taskHistoryForChart = uniqueTasks.slice(0, 200);
    
    // 保存到缓存
    localStorage.setItem(CACHE_KEY_TASKS, JSON.stringify(taskHistoryForChart));
    localStorage.setItem(CACHE_KEY_TIMESTAMP, Date.now().toString());
    
    console.log('任务历史缓存已更新，共', taskHistoryForChart.length, '条记录');
    
    // 更新图表
    if (summaryTrendChart) {
        initSummaryTrendChart();
    }
}

/**
 * 初始化总结趋势图（使用真实历史数据）
 */
function initSummaryTrendChart() {
    const ctx = document.getElementById('summaryTrendChart');
    if (!ctx) return;
    
    const labels = [];
    const data = [];
    const now = new Date();
    
    // 统计最近7天的总结次数
    const dailyCounts = {};
    
    for (let i = 6; i >= 0; i--) {
        const date = new Date(now);
        date.setDate(date.getDate() - i);
        const dateStr = date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' });
        labels.push(dateStr);
        dailyCounts[dateStr] = 0;
    }
    
    // 统计每天的任务次数（使用真实历史数据）
    if (taskHistoryForChart.length > 0) {
        taskHistoryForChart.forEach(function(task) {
            const taskDate = new Date(task.timestamp);
            const dateStr = taskDate.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' });
            if (dailyCounts.hasOwnProperty(dateStr)) {
                dailyCounts[dateStr]++;
            }
        });
    }
    
    // 获取统计结果
    labels.forEach(function(dateStr) {
        data.push(dailyCounts[dateStr] || 0);
    });
    
    summaryTrendChart = createChart('summaryTrendChart', 'line', {
        labels: labels,
        datasets: [{
            label: '总结次数',
            data: data,
            borderColor: '#667eea',
            backgroundColor: 'rgba(102, 126, 234, 0.1)',
            fill: true,
            tension: 0.4,
            pointBackgroundColor: '#667eea',
            pointBorderColor: '#fff',
            pointBorderWidth: 2,
            pointRadius: 5,
            pointHoverRadius: 7
        }]
    }, {
        plugins: {
            legend: {
                display: false
            }
        },
        scales: {
            y: {
                beginAtZero: true,
                ticks: {
                    stepSize: 5,
                    precision: 0
                }
            }
        }
    });
}

/**
 * 更新总结趋势图
 */
function updateSummaryChart(range) {
    const ranges = {
        '7d': { days: 7, label: '最近7天' },
        '30d': { days: 30, label: '最近30天' },
        '90d': { days: 90, label: '最近90天' }
    };
    
    const rangeInfo = ranges[range];
    if (!rangeInfo || !summaryTrendChart) return;
    
    const labels = [];
    const now = new Date();
    
    // 生成日期标签
    for (let i = rangeInfo.days - 1; i >= 0; i--) {
        const date = new Date(now);
        date.setDate(date.getDate() - i);
        labels.push(date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' }));
    }
    
    // 统计每天的任务次数（使用缓存的历史数据）
    const dailyCounts = {};
    labels.forEach(function(dateStr) {
        dailyCounts[dateStr] = 0;
    });
    
    if (taskHistoryForChart.length > 0) {
        taskHistoryForChart.forEach(function(task) {
            const taskDate = new Date(task.timestamp);
            const dateStr = taskDate.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' });
            if (dailyCounts.hasOwnProperty(dateStr)) {
                dailyCounts[dateStr]++;
            }
        });
    }
    
    const data = labels.map(function(dateStr) {
        return dailyCounts[dateStr] || 0;
    });
    
    summaryTrendChart.data.labels = labels;
    summaryTrendChart.data.datasets[0].data = data;
    summaryTrendChart.update();
    
    showToast('已更新为' + rangeInfo.label + '的数据', 'info');
}

/**
 * 初始化错误统计图
 */
function initErrorStatsChart() {
    const ctx = document.getElementById('errorStatsChart');
    if (!ctx) return;
    
    // 获取错误统计数据
    const errorStatsData = window.errorStatsData || {};
    
    const labels = ['ERROR', 'WARNING', 'INFO'];
    const data = [
        errorStatsData.error_count || 0,
        errorStatsData.warning_count || 0,
        errorStatsData.info_count || 0
    ];
    
    errorStatsChart = createChart('errorStatsChart', 'doughnut', {
        labels: labels,
        datasets: [{
            data: data,
            backgroundColor: ['#ef4444', '#f59e0b', '#0ea5e9'],
            borderWidth: 0
        }]
    }, {
        plugins: {
            legend: {
                position: 'right'
            }
        },
        cutout: '60%'
    });
}

/**
 * 加载最近活动
 */
async function loadRecentActivity() {
    try {
        const tasks = await apiGet('/api/get_recent_tasks', { limit: 5 });
        
        const timeline = document.getElementById('recentActivityTimeline');
        if (!timeline) return;
        
        if (tasks.length === 0) {
            timeline.innerHTML = `
                <div class="text-center py-4">
                    <i class="bi bi-inbox text-muted" style="font-size: 2rem;"></i>
                    <p class="mt-2 text-muted">暂无最近活动</p>
                </div>
            `;
            return;
        }
        
        let html = '';
        tasks.reverse().forEach(function(task, index) {
            const time = formatDate(task.timestamp, 'HH:mm:ss');
            const date = formatDate(task.timestamp, 'MM-DD');
            
            let iconClass = 'bi-info-circle';
            let colorClass = 'text-primary';
            
            if (task.task_type === '手动触发总结') {
                iconClass = 'bi-play-circle';
                colorClass = 'text-success';
            } else if (task.task_type === '定时任务') {
                iconClass = 'bi-clock';
                colorClass = 'text-info';
            } else if (task.status === '失败') {
                iconClass = 'bi-x-circle';
                colorClass = 'text-danger';
            }
            
            html += `
                <div class="timeline-item">
                    <div class="mb-1">
                        <small class="text-muted">${date} ${time}</small>
                    </div>
                    <div>
                        <i class="bi ${iconClass} ${colorClass} me-2"></i>
                        <strong>${task.channel.substring(0, 30)}${task.channel.length > 30 ? '...' : ''}</strong>
                    </div>
                    <div class="small text-muted mt-1">
                        ${task.task_type} - ${task.status}
                    </div>
                </div>
            `;
        });
        
        timeline.innerHTML = html;
        
        // 如果有新任务，更新缓存
        if (tasks.length > 0) {
            updateTaskHistoryCache(tasks);
        }
    } catch (error) {
        console.error('加载最近活动失败:', error);
    }
}

/**
 * 刷新机器人状态
 */
function refreshBotStatus() {
    const statusCard = document.getElementById('bot-status-card');
    if (!statusCard) return;
    
    statusCard.innerHTML = `
        <div class="spinner-border text-primary mb-3" role="status">
            <span class="visually-hidden">加载中...</span>
        </div>
        <p class="mb-0">正在获取机器人状态...</p>
    `;
    
    apiGet('/api/get_bot_status')
        .then(function(data) {
            if (data.status === 'success') {
                updateBotStatusUI(data);
                const lastUpdateEl = document.getElementById('lastUpdate');
                if (lastUpdateEl) {
                    lastUpdateEl.textContent = formatRelativeTime(new Date());
                }
            } else {
                statusCard.innerHTML = `
                    <div class="text-danger mb-3">
                        <i class="bi bi-exclamation-triangle" style="font-size: 2rem;"></i>
                    </div>
                    <p class="mb-0 text-danger">获取状态失败: ${data.message}</p>
                `;
            }
        })
        .catch(function(error) {
            statusCard.innerHTML = `
                <div class="text-danger mb-3">
                    <i class="bi bi-exclamation-triangle" style="font-size: 2rem;"></i>
                </div>
                    <p class="mb-0 text-danger">网络错误: ${error}</p>
            `;
        });
}

/**
 * 更新机器人状态UI
 */
function updateBotStatusUI(statusData) {
    const statusCard = document.getElementById('bot-status-card');
    if (!statusCard) return;
    
    const state = statusData.state;
    const stateName = statusData.state_name;
    const stateDescription = statusData.state_description;
    const stateIcon = statusData.state_icon;
    const stateColor = statusData.state_color;
    
    const colorMap = {
        'success': 'bg-success',
        'warning': 'bg-warning',
        'danger': 'bg-danger',
        'secondary': 'bg-secondary'
    };
    
    const badgeClass = colorMap[stateColor] || 'bg-secondary';
    const textClass = stateColor === 'success' ? 'text-success' : 
                      stateColor === 'warning' ? 'text-warning' : 'text-danger';
    
    statusCard.innerHTML = `
        <div class="${textClass} mb-3">
            <i class="bi bi-${stateIcon}" style="font-size: 3rem;"></i>
        </div>
        <h4 class="mb-1 fw-bold">${stateName}</h4>
        <p class="text-muted mb-2">${stateDescription}</p>
        <small class="text-muted">${formatDate(statusData.timestamp, 'HH:mm:ss')}</small>
    `;
    
    const pauseBtn = document.getElementById('pause-btn');
    const resumeBtn = document.getElementById('resume-btn');
    const shutdownBtn = document.getElementById('shutdown-btn');
    
    if (pauseBtn) pauseBtn.disabled = false;
    if (resumeBtn) resumeBtn.disabled = false;
    if (shutdownBtn) shutdownBtn.disabled = false;
    
    if (state === 'running') {
        if (pauseBtn) pauseBtn.disabled = false;
        if (resumeBtn) resumeBtn.disabled = true;
        if (shutdownBtn) shutdownBtn.disabled = false;
    } else if (state === 'paused') {
        if (pauseBtn) pauseBtn.disabled = true;
        if (resumeBtn) resumeBtn.disabled = false;
        if (shutdownBtn) shutdownBtn.disabled = false;
    } else if (state === 'shutting_down') {
        if (pauseBtn) pauseBtn.disabled = true;
        if (resumeBtn) resumeBtn.disabled = true;
        if (shutdownBtn) shutdownBtn.disabled = true;
    }
}

/**
 * 暂停机器人
 */
function pauseBot() {
    if (confirm('确定要暂停机器人吗？暂停后定时任务将停止，但手动命令仍可执行。')) {
        apiPost('/api/pause_bot')
            .then(function(data) {
                showToast(data.message, data.status === 'success' ? 'success' : 'danger');
                if (data.status === 'success') {
                    refreshBotStatus();
                }
            })
            .catch(function(error) {
                showToast('暂停失败: ' + error, 'danger');
            });
    }
}

/**
 * 恢复机器人
 */
function resumeBot() {
    if (confirm('确定要恢复机器人吗？恢复后定时任务将继续执行。')) {
        apiPost('/api/resume_bot')
            .then(function(data) {
                showToast(data.message, data.status === 'success' ? 'success' : 'danger');
                if (data.status === 'success') {
                    refreshBotStatus();
                }
            })
            .catch(function(error) {
                showToast('恢复失败: ' + error, 'danger');
            });
    }
}

/**
 * 关机机器人
 */
function shutdownBot() {
    if (confirm('⚠️ 确定要关机机器人吗？\n\n机器人将完全停止运行，需要手动重新启动。\n\n此操作不可逆！')) {
        apiPost('/api/shutdown_bot')
            .then(function(data) {
                showToast(data.message, data.status === 'success' ? 'success' : 'danger');
                if (data.status === 'success') {
                    refreshBotStatus();
                    const pauseBtn = document.getElementById('pause-btn');
                    const resumeBtn = document.getElementById('resume-btn');
                    const shutdownBtn = document.getElementById('shutdown-btn');
                    if (pauseBtn) pauseBtn.disabled = true;
                    if (resumeBtn) resumeBtn.disabled = true;
                    if (shutdownBtn) shutdownBtn.disabled = true;
                }
            })
            .catch(function(error) {
                showToast('关机失败: ' + error, 'danger');
            });
    }
}

/**
 * 重启机器人
 */
function restartBot() {
    if (confirm('确定要重启机器人吗？重启过程可能需要几秒钟。')) {
        apiPost('/api/restart_bot')
            .then(function(data) {
                showToast(data.message, data.status === 'success' ? 'success' : 'danger');
                if (data.status === 'success') {
                    setTimeout(function() {
                        refreshBotStatus();
                    }, 3000);
                }
            })
            .catch(function(error) {
                showToast('重启失败: ' + error, 'danger');
            });
    }
}

/**
 * 运行健康检查
 */
function runHealthCheck() {
    showToast('正在运行健康检查...', 'info');
    apiPost('/api/run_health_check')
        .then(function(data) {
            if (data.status === 'success') {
                showToast(data.message, 'success');
                setTimeout(function() {
                    location.reload();
                }, 2000);
            } else {
                showToast(data.message, 'danger');
            }
        })
        .catch(function(error) {
            showToast('健康检查失败: ' + error, 'danger');
        });
}

/**
 * 清空错误统计
 */
async function clearErrorStats() {
    if (confirm('确定要清空所有错误统计吗？此操作不可恢复。')) {
        const result = await apiPost('/api/clear_error_stats');
        if (result.status === 'success') {
            showToast(result.message, 'success');
            if (errorStatsChart) {
                errorStatsChart.data.datasets[0].data = [0, 0, 0];
                errorStatsChart.update();
            }
            setTimeout(function() {
                location.reload();
            }, 1000);
        } else {
            showToast(result.message, 'danger');
        }
    }
}

/**
 * 触发频道总结
 */
function triggerChannelSummary(channel) {
    if (confirm('确定要立即为频道 "' + channel + '" 生成总结报告吗？')) {
        const formData = new FormData();
        formData.append('channel', channel);
        
        fetch('/api/trigger_summary', {
            method: 'POST',
            body: formData
        })
        .then(function(response) { return response.json(); })
        .then(function(data) {
            showToast(data.message, data.status === 'success' ? 'success' : 'danger');
            loadRecentActivity();
        })
        .catch(function(error) {
            showToast('触发失败: ' + error, 'danger');
        });
    }
}

// 将错误统计数据暴露到全局（供HTML模板使用）
window.errorStatsData = {{ error_stats|tojson if error_stats else '{}' }};
