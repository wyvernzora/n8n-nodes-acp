#!/usr/bin/env node
'use strict';

const net = require('node:net');

const socketPath = process.argv[2];
const acpId = process.argv[3];
let buffer = '';
let proxyBuffer = '';
let nextId = 1;
let connectionId;
const pending = new Map();
const proxy = net.createConnection(socketPath);

process.stdin.on('data', (chunk) => {
	buffer = readLines(buffer + chunk.toString('utf8'), (line) => void handleMcp(line));
});

proxy.on('data', (chunk) => {
	proxyBuffer = readLines(proxyBuffer + chunk.toString('utf8'), (line) => {
		const message = JSON.parse(line);
		const request = pending.get(message.id);
		if (!request) {
			return;
		}
		pending.delete(message.id);
		if (message.error) {
			request.reject(new Error(message.error.message || 'ACP proxy request failed'));
		} else {
			request.resolve(message.result || {});
		}
	});
});

process.on('exit', () => {
	if (connectionId) {
		proxy.write(`${JSON.stringify({ id: nextId++, method: 'disconnect', params: { connectionId } })}\n`);
	}
});

async function handleMcp(line) {
	const message = parseJson(line);
	if (!message || message.id === undefined || typeof message.method !== 'string') {
		return;
	}

	try {
		switch (message.method) {
			case 'initialize':
				respond(message.id, {
					protocolVersion: message.params?.protocolVersion || '2024-11-05',
					capabilities: { tools: {} },
					serverInfo: { name: 'n8n-acp-tools', version: '0.0.0' },
				});
				return;
			case 'notifications/initialized':
				return;
			case 'ping':
				respond(message.id, {});
				return;
			case 'tools/list':
			case 'tools/call':
				await ensureConnected();
				respond(
					message.id,
					await requestProxy('message', {
						connectionId,
						method: message.method,
						params: message.params || {},
					}),
				);
				return;
			default:
				respondError(message.id, -32601, `Unknown MCP method: ${message.method}`);
		}
	} catch (error) {
		respondError(message.id, -32000, error.message || String(error));
	}
}

async function ensureConnected() {
	if (connectionId) {
		return;
	}
	const result = await requestProxy('connect', { acpId });
	connectionId = result.connectionId;
}

function requestProxy(method, params) {
	const id = nextId++;
	return new Promise((resolve, reject) => {
		pending.set(id, { resolve, reject });
		proxy.write(`${JSON.stringify({ id, method, params })}\n`);
	});
}

function respond(id, result) {
	process.stdout.write(`${JSON.stringify({ jsonrpc: '2.0', id, result })}\n`);
}

function respondError(id, code, message) {
	process.stdout.write(`${JSON.stringify({ jsonrpc: '2.0', id, error: { code, message } })}\n`);
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
	try {
		return JSON.parse(line);
	} catch {
		return undefined;
	}
}
