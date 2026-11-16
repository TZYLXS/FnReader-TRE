'use strict';
'require view';
'require form';
'require uci';
'require rpc';
'require ui';

var callStatus = rpc.declare({ object: 'fnparse', method: 'status', expect: {} });
var callTest = rpc.declare({ object: 'fnparse', method: 'test', expect: {} });
var callUSB = rpc.declare({ object: 'fnparse', method: 'usb', expect: {} });
var callLogs = rpc.declare({ object: 'fnparse', method: 'logs', params: [ 'lines' ], expect: {} });
var callClear = rpc.declare({ object: 'fnparse', method: 'clearcache', params: [ 'all' ], expect: {} });
var callRestart = rpc.declare({ object: 'fnparse', method: 'restart', expect: {} });

function fmtTime(ts) {
	if (!ts)
		return '—';
	var d = new Date(ts * 1000);
	return d.toLocaleString();
}

return view.extend({
	load: function() {
		return Promise.all([
			uci.load('fnparse'),
			L.resolveDefault(callStatus(), null)
		]);
	},

	renderStatus: function(st) {
		var self = this;

		if (!st)
			st = {};

		var dot = function(ok) {
			return E('span', {
				style: 'display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;background:' + (ok ? '#2e7d32' : '#c62828')
			});
		};

		var row = function(label, value) {
			return E('tr', {}, [
				E('td', { style: 'padding:3px 12px 3px 0;color:#666;white-space:nowrap' }, label),
				E('td', { style: 'padding:3px 0' }, value || '—')
			]);
		};

		var last = st.last;
		var lastText = last
			? '%s · %s · %s ms · %s'.format(last.type || '', last.name || last.sha1 || '', last.ms || 0, last.ok ? _('成功') : (last.note || _('失败')))
			: '—';

		var mounts = (st.mounts || []).filter(function(m) { return m.usb; });
		var mountRows = mounts.map(function(m) {
			return E('tr', {}, [
				E('td', { style: 'padding:2px 10px 2px 0' }, m.device),
				E('td', { style: 'padding:2px 10px 2px 0' }, m.mount),
				E('td', { style: 'padding:2px 10px 2px 0' }, m.fstype),
				E('td', { style: 'padding:2px 0' }, (m.freeMB || 0) + ' MB')
			]);
		});

		var usage = '—';
		if (st.cacheDir)
			usage = st.cacheDir + '（已用 ' + (st.cacheUsedMB || 0) + ' MB / 剩余 ' + (st.cacheFreeMB || 0) + ' MB）';

		return E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, _('运行状态')),
			E('table', { 'class': 'table' }, [
				E('tr', {}, [
					E('td', { style: 'padding:3px 12px 3px 0;color:#666' }, _('服务')),
					E('td', { style: 'padding:3px 0' }, [ dot(st.running), st.running ? _('运行中') : _('未运行') ])
				]),
				row(_('版本'), st.version),
				row(_('服务端'), st.server),
				row(_('令牌'), st.tokenSet ? _('已配置') : _('未配置')),
				row(_('设备 ID'), st.deviceId),
				row(_('设备名'), st.deviceName),
				row(_('服务端连通'), [ dot(st.serverOk), st.serverOk ? _('正常') : _('异常') ]),
				row(_('解析缓存'), usage),
				row(_('队列/完成/失败'), (st.inflight || 0) + ' / ' + (st.done || 0) + ' / ' + (st.failed || 0)),
				row(_('最近任务'), lastText),
				row(_('最近错误'), st.lastError ? (st.lastError + '（' + fmtTime(st.lastErrorAt) + '）') : ''),
				row(_('日志'), st.logPath)
			]),
			mounts.length ? E('div', {}, [
				E('h4', { style: 'margin-top:12px' }, _('检测到的 USB 存储')),
				E('table', { 'class': 'table' }, mountRows)
			]) : E('p', { 'class': 'cbi-section-descr' }, _('未检测到 USB 存储；插上 U 盘后刷新，或在下方手动填写缓存目录。')),
			E('div', { style: 'margin-top:12px;display:flex;gap:8px;flex-wrap:wrap' }, [
				E('button', {
					'class': 'btn cbi-button cbi-button-action',
					click: ui.createHandlerFn(self, function() {
						ui.showModal(_('测试连接'), [
							E('p', { 'class': 'spinning' }, _('正在连接服务端…'))
						]);
						return callTest().then(function(res) {
							ui.hideModal();
							ui.showModal(_('测试结果'), [
								E('pre', { style: 'white-space:pre-wrap;max-height:60vh;overflow:auto' }, JSON.stringify(res, null, 2)),
								E('div', { 'class': 'right' }, E('button', { 'class': 'btn', click: ui.hideModal }, _('关闭')))
							]);
						}).catch(function(err) {
							ui.hideModal();
							ui.addNotification(null, E('p', {}, _('测试失败：') + err), 'error');
						});
					})
				}, _('测试连接')),
				E('button', {
					'class': 'btn cbi-button',
					click: ui.createHandlerFn(self, function() {
						ui.showModal(_('运行日志'), [
							E('p', { 'class': 'spinning' }, _('读取日志…'))
						]);
						return callLogs(200).then(function(res) {
							ui.hideModal();
							ui.showModal(_('运行日志'), [
								E('pre', { style: 'white-space:pre-wrap;max-height:60vh;overflow:auto;font-size:12px' }, (res && res.log) || (res && res.error) || _('暂无日志')),
								E('div', { 'class': 'right' }, E('button', { 'class': 'btn', click: ui.hideModal }, _('关闭')))
							]);
						}).catch(function(err) {
							ui.hideModal();
							ui.addNotification(null, E('p', {}, _('读取日志失败：') + err), 'error');
						});
					})
				}, _('查看日志')),
				E('button', {
					'class': 'btn cbi-button cbi-button-remove',
					click: ui.createHandlerFn(self, function() {
						if (!confirm(_('清空全部解析缓存（含已解析的 JSON）？')))
							return;
						return callClear(true).then(function(res) {
							ui.addNotification(null, E('p', {}, _('已清理 ') + (res.removed || 0) + _(' 个文件，释放 ') + (res.freedMB || 0) + ' MB'), 'info');
						}).catch(function(err) {
							ui.addNotification(null, E('p', {}, _('清理失败：') + err), 'error');
						});
					})
				}, _('清空缓存')),
				E('button', {
					'class': 'btn cbi-button cbi-button-apply',
					click: ui.createHandlerFn(self, function() {
						return callRestart().then(function() {
							ui.addNotification(null, E('p', {}, _('引擎已重启')), 'info');
							setTimeout(function() { location.reload(); }, 1500);
						}).catch(function(err) {
							ui.addNotification(null, E('p', {}, _('重启失败：') + err), 'error');
						});
					})
				}, _('重启引擎')),
				E('button', {
					'class': 'btn cbi-button',
					click: function() { location.reload(); }
				}, _('刷新状态'))
			])
		]);
	},

	render: function(data) {
		var self = this;
		var st = data[1];

		var m = new form.Map('fnparse', _('FnReader 解析引擎'),
			_('把 FnReader 服务端的简介/封面任务挂到路由器：单线程长轮询领任务 → 按书源规则解析详情页（规则不命中时用通用网页解析兜底）→ 回传。引擎为纯静态 Go 二进制，安装包内自带全部运行环境。'));

		var s = m.section(form.NamedSection, 'main', 'fnparse', _('接入设置'));
		s.anonymous = true;

		var o = s.option(form.Flag, 'enabled', _('启用引擎'));
		o.rmempty = false;
		o.description = _('关闭后开机不会启动，等同禁用服务。');

		o = s.option(form.Value, 'server', _('接入 IP / 服务端地址'));
		o.placeholder = 'http://192.168.5.110:1122';
		o.rmempty = false;
		o.description = _('FnReader 服务端（legado-web）地址，带端口；不写 http:// 时自动补。');
		o.validate = function(section, value) {
			value = (value || '').trim();
			if (!value)
				return _('请填写接入 IP');
			if (!/^(https?:\/\/)?[A-Za-z0-9_.\-]+(:\d+)?(\/.*)?$/.test(value))
				return _('格式示例：http://192.168.5.110:1122');
			return true;
		};

		o = s.option(form.Value, 'token', _('Token'));
		o.password = true;
		o.rmempty = false;
		o.description = _('服务端「设置 → 访问令牌」里的 API Token，用于领任务与回传结果。');
		o.validate = function(section, value) {
			value = (value || '').trim();
			if (!value)
				return _('请填写 Token');
			return true;
		};

		o = s.option(form.Value, 'device_name', _('设备名'));
		o.placeholder = 'OpenWRT-LEDE';
		o.description = _('显示在服务端设备列表里，留空自动取主机名。');

		var s2 = s;

		o = s2.option(form.Value, 'cache_dir', _('解析缓存目录'));
		o.placeholder = '/mnt/sda1/fnparse';
		o.description = _('建议放在 USB 挂载点（如 /mnt/sda1/fnparse），避免写 flash。留空且开启自动检测时自动选第一个可写 USB 盘。');

		o = s2.option(form.Flag, 'usb_autodetect', _('自动检测 USB'));
		o.default = '1';
		o.rmempty = false;
		o.description = _('未填缓存目录时，自动在 /mnt/* 下寻找可写 USB 存储。');

		o = s2.option(form.Value, 'poll_wait', _('长轮询等待（秒）'));
		o.datatype = 'range(0,30)';
		o.placeholder = '20';
		o.description = _('每次 pending 请求挂起时长，服务端上限 30 秒。');

		o = s2.option(form.Value, 'part_max_mb', _('分片上限（MB）'));
		o.datatype = 'range(1,8)';
		o.placeholder = '2';
		o.description = _('正文分片按 50 章或该字节数切分，不拆章；协议上限 8 MB。');

		o = s2.option(form.Value, 'cache_max_mb', _('缓存上限（MB）'));
		o.placeholder = '2048';
		o.description = _('解析缓存超过后按最旧文件清理；0 表示不限制。');

		o = s2.option(form.Value, 'tmp_ttl_hours', _('原文件保留（小时）'));
		o.placeholder = '48';
		o.description = _('下载的原书临时文件保留时长，过期随清理删除。');

		var s3 = s;

		o = s3.option(form.ListValue, 'log_level', _('日志级别'));
		o.value('debug', 'debug');
		o.value('info', 'info');
		o.value('warn', 'warn');
		o.value('error', 'error');
		o.default = 'info';

		o = s3.option(form.Value, 'claim_types', _('领取任务类型（写死）'));
		o.placeholder = 'cover,intro,bookinfo';
		o.description = _('已写死 cover/intro/bookinfo：插件只承接简介与封面（媒体类），固定单线程、每次只领 1 个；详情页优先按服务端书源规则解析，规则不命中时用通用网页解析兜底。');

		var formNode = m.render();
		return Promise.resolve(formNode).then(function(node) {
			return E('div', {}, [ self.renderStatus(st), node ]);
		});
	}
});
