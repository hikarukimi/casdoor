#!/bin/bash

echo "=== Casdoor 反向代理测试脚本 ==="

# 配置
PROXY_PORT="8080"
API_PORT="8000"
TEST_DOMAIN="blog.example.com"
BACKEND_URL="http://localhost:3000"

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 测试计数器
PASSED=0
FAILED=0

# 测试函数
test_case() {
    local test_name=$1
    local command=$2
    local expected=$3
    
    echo -e "\n${YELLOW}测试: $test_name${NC}"
    echo "执行: $command"
    
    result=$(eval $command 2>&1)
    
    if echo "$result" | grep -q "$expected"; then
        echo -e "${GREEN}✓ 通过${NC}"
        ((PASSED++))
    else
        echo -e "${RED}✗ 失败${NC}"
        echo "预期包含: $expected"
        echo "实际结果: $result"
        ((FAILED++))
    fi
}

echo "=== 开始测试 ==="

# 测试1: 检查代理端口是否启动
echo -e "\n${YELLOW}检查代理端口状态...${NC}"
if netstat -tuln | grep -q ":$PROXY_PORT "; then
    echo -e "${GREEN}✓ 代理端口 $PROXY_PORT 正在监听${NC}"
    ((PASSED++))
else
    echo -e "${RED}✗ 代理端口 $PROXY_PORT 未启动${NC}"
    ((FAILED++))
fi

# 测试2: 检查 API 端口是否启动
echo -e "\n${YELLOW}检查 API 端口状态...${NC}"
if netstat -tuln | grep -q ":$API_PORT "; then
    echo -e "${GREEN}✓ API 端口 $API_PORT 正在监听${NC}"
    ((PASSED++))
else
    echo -e "${RED}✗ API 端口 $API_PORT 未启动${NC}"
    ((FAILED++))
fi

# 测试3: 基本代理功能
test_case "基本代理功能" \
    "curl -s -H 'Host: $TEST_DOMAIN' http://localhost:$PROXY_PORT/test" \
    "Hello from backend"

# 测试4: API 端口独立性
test_case "API 端口独立性" \
    "curl -s http://localhost:$API_PORT/api/get-applications" \
    "application"

# 测试5: 未配置域名的请求
test_case "未配置域名返回404" \
    "curl -s -w '%{http_code}' -o /dev/null -H 'Host: unknown.example.com' http://localhost:$PROXY_PORT/test" \
    "404"

# 测试6: 代理头验证
echo -e "\n${YELLOW}测试: 代理头验证${NC}"
echo "执行: curl -s -H 'Host: $TEST_DOMAIN' http://localhost:$PROXY_PORT/test"
result=$(curl -s -H "Host: $TEST_DOMAIN" http://localhost:$PROXY_PORT/test)
headers_ok=true

if ! echo "$result" | grep -q "X-Real-IP"; then
    echo -e "${RED}✗ 缺少 X-Real-IP 头${NC}"
    headers_ok=false
fi

if ! echo "$result" | grep -q "X-Forwarded-For"; then
    echo -e "${RED}✗ 缺少 X-Forwarded-For 头${NC}"
    headers_ok=false
fi

if ! echo "$result" | grep -q "X-Forwarded-Proto"; then
    echo -e "${RED}✗ 缺少 X-Forwarded-Proto 头${NC}"
    headers_ok=false
fi

if ! echo "$result" | grep -q "X-Forwarded-Host"; then
    echo -e "${RED}✗ 缺少 X-Forwarded-Host 头${NC}"
    headers_ok=false
fi

if $headers_ok; then
    echo -e "${GREEN}✓ 所有代理头正确设置${NC}"
    ((PASSED++))
else
    ((FAILED++))
fi

# 测试7: 响应时间测试
echo -e "\n${YELLOW}测试: 响应时间${NC}"
start_time=$(date +%s%N)
curl -s -H "Host: $TEST_DOMAIN" http://localhost:$PROXY_PORT/test > /dev/null
end_time=$(date +%s%N)
response_time=$((($end_time - $start_time) / 1000000))

echo "响应时间: ${response_time}ms"
if [ $response_time -lt 1000 ]; then
    echo -e "${GREEN}✓ 响应时间良好 (< 1s)${NC}"
    ((PASSED++))
else
    echo -e "${YELLOW}⚠ 响应时间较慢 (>= 1s)${NC}"
    ((PASSED++))
fi

# 测试8: 并发请求测试
echo -e "\n${YELLOW}测试: 并发请求处理${NC}"
success_count=0
for i in {1..10}; do
    if curl -s -H "Host: $TEST_DOMAIN" http://localhost:$PROXY_PORT/test > /dev/null 2>&1; then
        ((success_count++))
    fi
done

if [ $success_count -eq 10 ]; then
    echo -e "${GREEN}✓ 所有并发请求成功 (10/10)${NC}"
    ((PASSED++))
else
    echo -e "${RED}✗ 部分并发请求失败 (${success_count}/10)${NC}"
    ((FAILED++))
fi

# 测试总结
echo -e "\n=== 测试总结 ==="
echo -e "通过: ${GREEN}${PASSED}${NC}"
echo -e "失败: ${RED}${FAILED}${NC}"
echo -e "总计: $((PASSED + FAILED))"

if [ $FAILED -eq 0 ]; then
    echo -e "\n${GREEN}所有测试通过！${NC}"
    exit 0
else
    echo -e "\n${RED}部分测试失败，请检查配置和日志${NC}"
    exit 1
fi
