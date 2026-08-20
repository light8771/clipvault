import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(fileURLToPath(import.meta.url));
const read = name => readFileSync(join(root, name), 'utf8');

// ── 数据接口页：结构保留 + 敏感数据默认授权开关（T06 回归）──────────

test('data-api page keeps its contract and exposes the sensitive default switch', () => {
  const html = read('pages/page-data-api.html');
  for (const marker of ['dapiPage', 'dapiEnabled', 'dapiPort', 'dapiTokenName', 'dapiTokenExpiry', 'dapiTokenList', 'dapiAuditList']) {
    assert.ok(html.includes(`id="${marker}"`), `page-data-api.html must contain #${marker}`);
  }
  assert.ok(html.includes('id="dapiSensitiveDefault"'), 'settings must expose the sensitive default switch');
  assert.ok(html.includes('敏感数据默认授权'), 'sensitive default label must be present');
  assert.ok(!html.includes('dapiTokenAllowSensitive'), 'token creation must not carry a per-token sensitive checkbox');
  // 不允许出现按内容类型（文本/图片/文件）的权限开关。
  for (const forbidden of ['允许文本', '允许图片', '允许文件', '允许文本类型', 'textPermission', 'imagePermission', 'filePermission']) {
    assert.ok(!html.includes(forbidden), `content-type permission control "${forbidden}" must not exist`);
  }
});

// ── 菜单与页面注册（MCP 顶级菜单 + 页面定义 + 模块资产）─────

test('settings menu exposes an independent MCP entry beside the data API entry', () => {
  const menu = read('pages/settings-menu.html');
  assert.ok(menu.includes('href="/data-api"'), '数据接口 menu entry must remain');
  assert.ok(menu.includes('数据接口'), '数据接口 label must remain');
  assert.ok(menu.includes('href="/mcp"'), 'MCP menu entry must exist');
  assert.ok(menu.includes('eq .Menu "mcp"'), 'MCP menu highlight key must exist');
});

test('server static wiring registers the MCP page, module and stylesheet', () => {
  const serverStatic = read('../server_static.go');
  assert.ok(serverStatic.includes('"/mcp"'), 'page route /mcp must be registered');
  assert.ok(serverStatic.includes('page-mcp.html'), 'page-mcp.html must be assembled');
  assert.ok(serverStatic.includes('"/app/mcp.js"'), 'mcp.js must be a module asset');
  const server = read('../server.go');
  assert.ok(server.includes('"/settings-mcp.css"'), 'settings-mcp.css must be served');
});

// ── MCP 页：必备功能块（服务/状态/地址/URL 复制/令牌/能力/审计）──

test('mcp page contains all required functional blocks', () => {
  const html = read('pages/page-mcp.html');
  for (const marker of [
    'mcpPage',           // 页面根（模块初始化探测点）
    'mcpStatusText',     // 运行状态
    'mcpEnabled',        // 启用开关
    'mcpPort',           // 监听地址
    'mcpUrlText',        // MCP URL 复制
    'mcpTokenList',      // 令牌列表
    'mcpTokenName',      // 令牌创建
    'mcpTokenExpiry',
    'mcpSensitiveDefault', // 敏感数据默认授权（全局默认）
    'mcpToolList',       // 能力清单：Tools
    'mcpResourceList',   // 能力清单：Resources
    'mcpConfigExample',  // 接入配置示例
    'mcpAuditList',      // MCP 审计
    'mcpFilterOp',
    'mcpFilterToken',
  ]) {
    assert.ok(html.includes(`id="${marker}"`), `page-mcp.html must contain #${marker}`);
  }
  for (const action of ['mcp-save', 'mcp-copy-url', 'mcp-token-create', 'mcp-audit-export', 'mcp-audit-clear']) {
    assert.ok(html.includes(`data-action="${action}"`), `page-mcp.html must wire ${action}`);
  }
  // 不允许出现按内容类型的权限开关。
  for (const forbidden of ['允许文本', '允许图片', '允许文件', 'textPermission', 'imagePermission', 'filePermission']) {
    assert.ok(!html.includes(forbidden), `content-type permission control "${forbidden}" must not exist`);
  }
});

// ── 模块行为：创建令牌继承全局默认；敏感授权按令牌单独覆盖；审计支持筛选/导出/清空 ──

test('mcp module inherits sensitiveDefault on create and covers per-token override + audit flows', () => {
  const source = read('app/mcp.js');
  assert.ok(source.includes("'/api/v1/mcp/tokens'"), 'token create endpoint');
  assert.ok(!source.includes('mcpTokenAllowSensitive'), 'create payload must not carry a per-token sensitive flag');
  assert.ok(source.includes("sensitiveDefault: el('mcpSensitiveDefault').checked"), 'config save carries sensitiveDefault');
  assert.ok(source.includes("'/api/v1/mcp/tokens/sensitive'"), 'per-token sensitive override endpoint');
  assert.ok(source.includes("data-action=\"mcp-token-sensitive\""), 'per-token sensitive toggle (dynamically rendered)');
  assert.ok(source.includes("'/api/v1/mcp/config'"), 'config save endpoint');
  assert.ok(source.includes("data-action=\"mcp-token-regenerate\""), 'token regenerate flow (dynamically rendered)');
  assert.ok(source.includes("data-action=\"mcp-token-revoke\""), 'token revoke flow (dynamically rendered)');
  const audit = read('app/mcp-audit.js');
  assert.ok(audit.includes("'/api/v1/mcp/audit'"), 'audit list endpoint');
  assert.ok(audit.includes('/api/v1/mcp/audit/export'), 'audit export endpoint');
});

test('data-api module inherits sensitiveDefault on create and exposes the per-token override', () => {
  const source = read('app/data-api.js');
  assert.ok(source.includes("'/api/v1/data-api/tokens'"), 'token create endpoint');
  assert.ok(!source.includes('dapiTokenAllowSensitive'), 'create payload must not carry a per-token sensitive flag');
  assert.ok(source.includes("sensitiveDefault: el('dapiSensitiveDefault').checked"), 'config save carries sensitiveDefault');
  assert.ok(source.includes("'/api/v1/data-api/tokens/sensitive'"), 'per-token sensitive override endpoint');
  assert.ok(source.includes("data-action=\"data-api-token-sensitive\""), 'per-token sensitive toggle (dynamically rendered)');
});

test('settings bootstrap registers the MCP module and all its actions', () => {
  const source = read('app/settings-bootstrap.js');
  assert.ok(source.includes("from './mcp.js'"), 'mcp module import');
  assert.ok(source.includes('registerMCP()'), 'mcp module registration');
  for (const action of ['mcp-save', 'mcp-token-create', 'mcp-token-sensitive', 'mcp-token-regenerate', 'mcp-token-revoke', 'mcp-audit-export', 'mcp-audit-clear']) {
    assert.ok(source.includes(`'${action}'`), `bootstrap action ${action}`);
  }
});

// 令牌卡片是动态渲染的：模块里出现的每个 data-action 都必须在 bootstrap 注册，
// 否则按钮渲染出来但点击无响应。
test('bootstrap registers every dynamically rendered token action', () => {
  const bootstrap = read('app/settings-bootstrap.js');
  for (const module of ['app/data-api.js', 'app/mcp.js']) {
    const source = read(module);
    const actions = [...source.matchAll(/data-action="([a-z-]+)"/g)].map(m => m[1]);
    for (const action of actions) {
      assert.ok(bootstrap.includes(`'${action}'`), `${module} renders #${action} but bootstrap does not register it`);
    }
  }
});

test('audit renderers surface the sensitive-accessed marker', () => {
  const mcpAudit = read('app/mcp-audit.js');
  assert.ok(mcpAudit.includes('sensitiveAccessed'), 'mcp audit rows must render sensitiveAccessed');
  const dataAPI = read('app/data-api-audit.js');
  assert.ok(dataAPI.includes('sensitiveAccessed'), 'data-api audit rows must render sensitiveAccessed');
});
