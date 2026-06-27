#!/usr/bin/env node
'use strict';

const childProcess = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');

const host = process.env.ACP_HOST || '127.0.0.1';
const port = Number(process.env.ACP_PORT || '8080');
const cwd = process.env.OPENCODE_CWD || '/workspace';
const bridgeScript = process.env.ACP_MCP_STDIO_BRIDGE || '/usr/local/bin/acp-mcp-stdio-bridge';

net.createServer(handleAcpConnection).listen(port, host);

function handleAcpConnection(socket) {
	const bridgeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'acp-mcp-'));
	const bridgeSocket = path.join(bridgeDir, 'bridge.sock');
	const pending = new Map();
	let nextId = 1;
	let clientBuffer = '';
	let childBuffer = '';

	const bridgeServer = net.createServer((bridge) => handleBridgeConnection(bridge, socket, pending, () => nextId++));
	bridgeServer.listen(bridgeSocket);

	const child = childProcess.spawn('opencode', ['acp', '--cwd', cwd], {
		stdio: ['pipe', 'pipe', 'pipe'],
	});

	const cleanup = () => {
		child.kill();
		bridgeServer.close();
		for (const request of pending.values()) {
			clearTimeout(request.timer);
			request.reject(new Error('ACP connection closed'));
		}
		pending.clear();
		fs.rmSync(bridgeDir, { force: true, recursive: true });
	};

	socket.on('close', cleanup);
	socket.on('error', cleanup);
	child.on('close', () => socket.end());
	child.on('error', () => socket.end());
	child.stderr.on('data', (chunk) => process.stderr.write(chunk));

	child.stdout.on('data', (chunk) => {
		childBuffer = readLines(childBuffer + chunk.toString('utf8'), (line) => socket.write(`${line}\n`));
	});

	socket.on('data', (chunk) => {
		clientBuffer = readLines(clientBuffer + chunk.toString('utf8'), (line) => {
			const message = parseJson(line);
			if (!message) {
				child.stdin.write(`${line}\n`);
				return;
			}
			if (message && pending.has(message.id) && message.method === undefined) {
				const request = pending.get(message.id);
				pending.delete(message.id);
				clearTimeout(request.timer);
				if (message.error) {
					request.reject(new Error(message.error.message || 'ACP request failed'));
				} else {
					request.resolve(message.result || {});
				}
				return;
			}

			child.stdin.write(`${JSON.stringify(rewriteSessionNew(message, bridgeSocket))}\n`);
		});
	});
}

function handleBridgeConnection(bridge, acpSocket, pending, nextId) {
	let buffer = '';

	bridge.on('data', (chunk) => {
		buffer = readLines(buffer + chunk.toString('utf8'), (line) => {
			const message = parseJson(line);
			if (!message || typeof message.method !== 'string') {
				return;
			}
			requestAcp(acpSocket, pending, nextId, acpMethod(message.method), message.params || {})
				.then((result) => bridge.write(`${JSON.stringify({ id: message.id, result })}\n`))
				.catch((error) =>
					bridge.write(`${JSON.stringify({ id: message.id, error: { message: error.message } })}\n`),
				);
		});
	});
}

function acpMethod(method) {
	switch (method) {
		case 'connect':
			return 'mcp/connect';
		case 'message':
			return 'mcp/message';
		case 'disconnect':
			return 'mcp/disconnect';
		default:
			return method;
	}
}

function requestAcp(socket, pending, nextId, method, params) {
	const id = `proxy:${nextId()}`;
	const timer = setTimeout(() => {
		const request = pending.get(id);
		if (request) {
			pending.delete(id);
			request.reject(new Error(`ACP request timed out: ${method}`));
		}
	}, 600000);

	return new Promise((resolve, reject) => {
		pending.set(id, { resolve, reject, timer });
		socket.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
	});
}

function rewriteSessionNew(message, socketPath) {
	if (!message || message.method !== 'session/new' || !message.params || !Array.isArray(message.params.mcpServers)) {
		return message;
	}

	return {
		...message,
		params: {
			...message.params,
			mcpServers: message.params.mcpServers.map((server) => rewriteMcpServer(server, socketPath)),
		},
	};
}

function rewriteMcpServer(server, socketPath) {
	if (!server || server.type !== 'acp') {
		return server;
	}
	return {
		type: 'stdio',
		name: server.name || 'n8n-tools',
		command: process.execPath,
		args: [bridgeScript, socketPath, server.id],
		env: [],
	};
}

function readLines(buffer, onLine) {
	for (;;) {
		const index = buffer.indexOf('\n');
		if (index === -1) {
			return buffer;
		}
		const line = buffer.slice(0, index).trim();
		buffer = buffer.slice(index + 1);
		if (line) {
			onLine(line);
		}
	}
}

function parseJson(line) {
	if (!line.startsWith('{')) {
		return undefined;
	}
	try {
		return JSON.parse(line);
	} catch {
		return undefined;
	}
}
