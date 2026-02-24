const http = require('http');

const server = http.createServer((req, res) => {
  console.log('=== 收到请求 ===');
  console.log('时间:', new Date().toISOString());
  console.log('方法:', req.method);
  console.log('URL:', req.url);
  console.log('Host:', req.headers.host);
  console.log('');
  console.log('请求头:');
  for (const [key, value] of Object.entries(req.headers)) {
    console.log(`  ${key}: ${value}`);
  }
  console.log('');

  // 验证代理头
  const proxyHeaders = {
    'X-Real-IP': req.headers['x-real-ip'],
    'X-Forwarded-For': req.headers['x-forwarded-for'],
    'X-Forwarded-Proto': req.headers['x-forwarded-proto'],
    'X-Forwarded-Host': req.headers['x-forwarded-host']
  };

  console.log('代理头验证:');
  for (const [key, value] of Object.entries(proxyHeaders)) {
    const status = value ? '✓' : '✗';
    console.log(`  ${status} ${key}: ${value || '未设置'}`);
  }
  console.log('');

  // 返回响应
  const responseData = {
    message: 'Hello from backend server!',
    timestamp: new Date().toISOString(),
    method: req.method,
    url: req.url,
    headers: req.headers,
    proxyHeaders: proxyHeaders,
    proxyHeadersValid: Object.values(proxyHeaders).every(v => v !== undefined)
  };

  res.writeHead(200, { 
    'Content-Type': 'application/json',
    'Access-Control-Allow-Origin': '*'
  });
  res.end(JSON.stringify(responseData, null, 2));
});

const PORT = 3000;
server.listen(PORT, () => {
  console.log(`测试服务器运行在 http://localhost:${PORT}`);
  console.log('等待请求...\n');
});

// 优雅关闭
process.on('SIGTERM', () => {
  console.log('收到 SIGTERM 信号，正在关闭服务器...');
  server.close(() => {
    console.log('服务器已关闭');
    process.exit(0);
  });
});
