#!/usr/bin/env node
'use strict';

const assert = require('node:assert/strict');
const net = require('node:net');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const path = require('node:path');

const harnessPath = path.join(__dirname, 'harness', 'fake-acp-harness.js');

main().catch((error) => {
	console.error(error);
	process.exitCode = 1;
});

async function main() {
	const port = await freePort();
	const harness = spawn(process.execPath, [harnessPath], {
		env: { ...process.env, ACP_PORT: String(port) },
		stdio: ['ignore', 'pipe', 'pipe'],
	});
	const stderr = [];
	harness.stderr.on('data', (chunk) => stderr.push(chunk.toString('utf8')));

	try {
		await waitForHarness(harness, port);
		const client = await RpcClient.connect('127.0.0.1', port);
		const chunks = [];
		client.onNotify((message) => {
			if (message.method === 'session/update') {
				chunks.push(message.params.update.content.text);
			}
		});
		client.onRequest('mcp/connect', () => ({ connectionId: 'self-check-tools' }));
		client.onRequest('mcp/message', (params) => {
			if (params.method === 'tools/list') {
				return {
					tools: [
						{
							name: 'echo_tool',
							description: 'Return a deterministic e2e result',
							inputSchema: { type: 'object', properties: { input: { type: 'string' } }, required: ['input'] },
						},
					],
				};
			}
			if (params.method === 'tools/call') {
				assert.equal(params.params.name, 'echo_tool');
				assert.deepEqual(params.params.arguments, { input: 'e2e' });
				return { content: [{ type: 'text', text: 'tool-ok' }] };
			}
			throw new Error(`unexpected MCP method: ${params.method}`);
		});
		client.onRequest('mcp/disconnect', () => ({}));

		await client.request('initialize', { protocolVersion: 1, clientCapabilities: {}, clientInfo: { name: 'self-check' } });
		const session = await client.request('session/new', {
			cwd: '/workspace',
			mcpServers: [{ type: 'acp', name: 'n8n-tools', id: 'tools-1' }],
		});
		await client.request('session/prompt', {
			sessionId: session.sessionId,
			prompt: [{ type: 'text', text: 'CALL_TOOL' }],
		});

		assert.equal(chunks.join(''), 'tool:tool-ok');
		client.close();
		console.log('e2e self-check passed');
	} finally {
		harness.kill('SIGTERM');
		await once(harness, 'exit').catch(() => {});
		if (process.exitCode) {
			console.error(stderr.join(''));
		}
	}
}

async function freePort() {
	const server = net.createServer();
	server.listen(0, '127.0.0.1');
	await once(server, 'listening');
	const address = server.address();
	const port = address.port;
	server.close();
	await once(server, 'close');
	return port;
}

async function waitForHarness(child, port) {
	const deadline = Date.now() + 5000;
	while (Date.now() < deadline) {
		if (child.exitCode !== null) throw new Error(`fake harness exited early with ${child.exitCode}`);
		try {
			const socket = net.createConnection(port, '127.0.0.1');
			await once(socket, 'connect');
			socket.end();
			return;
		} catch {
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
	}
	throw new Error('timed out waiting for fake harness');
}

class RpcClient {
	static connect(host, port) {
		const socket = net.createConnection(port, host);
		return once(socket, 'connect').then(() => new RpcClient(socket));
	}

	constructor(socket) {
		this.socket = socket;
		this.buffer = '';
		this.nextId = 1;
		this.pending = new Map();
		this.requestHandlers = new Map();
		this.notifyHandlers = [];

		socket.on('data', (chunk) => this.read(chunk));
		socket.on('error', (error) => this.rejectAll(error));
		socket.on('close', () => this.rejectAll(new Error('connection closed')));
	}

	request(method, params) {
		const id = this.nextId++;
		this.write({ jsonrpc: '2.0', id, method, params });
		return new Promise((resolve, reject) => {
			this.pending.set(id, { resolve, reject });
		});
	}

	onRequest(method, handler) {
		this.requestHandlers.set(method, handler);
	}

	onNotify(handler) {
		this.notifyHandlers.push(handler);
	}

	close() {
		this.socket.end();
	}

	read(chunk) {
		this.buffer += chunk.toString('utf8');
		for (;;) {
			const newline = this.buffer.indexOf('\n');
			if (newline === -1) return;

			const raw = this.buffer.slice(0, newline).trim();
			this.buffer = this.buffer.slice(newline + 1);
			if (raw !== '') this.dispatch(JSON.parse(raw));
		}
	}

	dispatch(message) {
		if (message.method !== undefined && message.id !== undefined) {
			void this.handleRequest(message);
			return;
		}
		if (message.method !== undefined) {
			for (const handler of this.notifyHandlers) handler(message);
			return;
		}

		const pending = this.pending.get(message.id);
		if (pending === undefined) return;
		this.pending.delete(message.id);
		if (message.error !== undefined) {
			pending.reject(new Error(message.error.message || `request failed: ${message.error.code}`));
		} else {
			pending.resolve(message.result || {});
		}
	}

	async handleRequest(message) {
		const handler = this.requestHandlers.get(message.method);
		if (handler === undefined) {
			this.write({ jsonrpc: '2.0', id: message.id, error: { code: -32601, message: `unknown method: ${message.method}` } });
			return;
		}
		try {
			this.write({ jsonrpc: '2.0', id: message.id, result: await handler(message.params || {}) });
		} catch (error) {
			this.write({
				jsonrpc: '2.0',
				id: message.id,
				error: { code: -32000, message: error instanceof Error ? error.message : String(error) },
			});
		}
	}

	write(message) {
		this.socket.write(`${JSON.stringify(message)}\n`);
	}

	rejectAll(error) {
		for (const pending of this.pending.values()) pending.reject(error);
		this.pending.clear();
	}
}
