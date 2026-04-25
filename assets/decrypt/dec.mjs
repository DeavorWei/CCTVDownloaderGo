"use strict";
import url from "url";
import fs from "fs";
import fsPromises from "fs/promises";
import path from "path";
import process from "process";
import events from "events";
import os from "os";

const NAL_START_FIRST = Buffer.from("00000001", "hex");
const NAL_START_SECOND = Buffer.from("000001", "hex");
const __dirname = path.dirname(url.fileURLToPath(import.meta.url));

// 并发线程数，默认使用CPU核心数，最多8个
const THREAD_COUNT = Math.min(os.cpus().length, 8);

function NALUnit(data) {
    if (data.subarray(0, 4).equals(NAL_START_FIRST)) {
        this.start = data.subarray(0, 4);
        this.header = data[4];
        this.data = data.subarray(5);
    } else if (data.subarray(0, 3).equals(NAL_START_SECOND)) {
        this.start = data.subarray(0, 3);
        this.header = data[3];
        this.data = data.subarray(4);
    } else {
        throw new Error("NAL单元的起始码不匹配");
    }

    this.forbiddenZeroBit = this.header >> 7;
    this.nalRefIdc = this.header >> 5 & 0x3;
    this.nalUnitType = this.header & 0x1F;
}

NALUnit.prototype.reload = function (data) {
    this.header = data[0];
    this.data = data.subarray(1);

    this.forbiddenZeroBit = this.header >> 7;
    this.nalRefIdc = this.header >> 5 & 0x3;
    this.nalUnitType = this.header & 0x1F;
};

NALUnit.prototype.dump = function () {
    return Buffer.concat([this.start, Buffer.from([this.header]), this.data]);
}

function getNaluPos(buf) {
    let start, prev = 0, off = 0;
    const ret = [];

    while ((start = buf.indexOf(Buffer.from("0000", "hex"), off + 2)) !== -1) {
        switch (buf[start + 2]) {
            case 0:
                if (buf[start + 3] === 1) {
                    ret.push([prev, start]);
                    prev = start;
                }
                break;
            case 1:
                ret.push([prev, start]);
                prev = start;
                break;
        }
        off = start;
    }

    ret.push([prev, buf.length]);

    return ret;
}

/**
 * 输出进度到stderr（供Go端解析）
 * 格式: PROGRESS:current:total
 */
function reportProgress(current, total) {
    process.stderr.write(`PROGRESS:${current}:${total}\n`);
}

async function mainDecrypter(CNTVH5PlayerModule) {
    let curDate = Date.now().toString();
    const MemoryExtend = 2048;
    let vmpTag = '';

    function _common(o) {
        const memory = CNTVH5PlayerModule._jsmalloc(curDate.length + MemoryExtend);

        CNTVH5PlayerModule.HEAP8.fill(0, memory, memory + curDate.length + MemoryExtend);
        CNTVH5PlayerModule.HEAP8.set(Array.from(curDate, e => e.charCodeAt(0)), memory);

        let ret;
        switch (o) {
            case "InitPlayer":
                ret = CNTVH5PlayerModule._CNTV_InitPlayer(memory);
                break;
            case "UnInitPlayer":
                ret = CNTVH5PlayerModule._CNTV_UnInitPlayer(memory);
                break;
            case "UpdatePlayer":
                vmpTag = CNTVH5PlayerModule._CNTV_UpdatePlayer(memory).toString(16);
                vmpTag = ['0'.repeat(8 - vmpTag.length), vmpTag].join('');
                ret = 0;
                break;
        }

        CNTVH5PlayerModule._jsfree(memory);
        return ret;
    }

    function InitPlayer() { return _common("InitPlayer"); }
    function UnInitPlayer() { return _common("UnInitPlayer"); }
    function UpdatePlayer() { return _common("UpdatePlayer"); }

    function decrypt(buf) {
        const pageHost = "https://tv.cctv.com";
        const addr = CNTVH5PlayerModule._jsmalloc(buf.length + MemoryExtend);
        const StaticCallModuleVod = {
            H264NalSet: function (e, t, i, n, r) { return e._CNTV_jsdecVOD7(t, i, n, r); },
            H265NalData: function (e, t, i, n, r) { return e._CNTV_jsdecVOD6(t, i, n, r); },
            AVS1AudioKey: function (e, t, i, n, r) { return e._CNTV_jsdecVOD5(t, i, n, r); },
            HEVC2AAC: function (e, t, i, n, r) { return e._CNTV_jsdecVOD4(t, i, n, r); },
            HASHMap: function (e, t, i, n, r) { return e._CNTV_jsdecVOD3(t, i, n, r); },
            BASE64Dec: function (e, t, i, n, r) { return e._CNTV_jsdecVOD2(t, i, n, r); },
            MediaSession: function (e, t, i, n, r) { return e._CNTV_jsdecVOD1(t, i, n, r); },
            Mp4fragment: function (e, t, i, n, r) { return e._CNTV_jsdecVOD0(t, i, n, r); },
            MpegAudio: function (e, t, i, n, r) { return e._CNTV_jsdecVOD8(t, i, n, r); },
            AACDemuxer: function (e, t, i, n, r) { return e._jsdecVOD(i, n, r); }
        };
        function StaticCallModuleVodAPI(e, t, i, n, r, a) {
            return StaticCallModuleVod[a](e, t, i, n, r);
        }

        CNTVH5PlayerModule.HEAP8.set(buf, addr);
        CNTVH5PlayerModule.HEAP8.set(
            Array.from(pageHost, e => e.charCodeAt(0)), addr + buf.length
        );
        const addr2 = CNTVH5PlayerModule._jsmalloc(curDate.length);
        CNTVH5PlayerModule.HEAP8.set(Array.from(curDate, e => e.charCodeAt(0)), addr2);

        for (const i in vmpTag) {
            if ("0123456".includes(vmpTag[i])) {
                StaticCallModuleVodAPI(
                    CNTVH5PlayerModule, addr2, addr, buf.length, pageHost.length, Object.keys(StaticCallModuleVod)[i]
                );
            }
        }

        const decRet = StaticCallModuleVodAPI(
            CNTVH5PlayerModule, addr2, addr, buf.length, pageHost.length, Object.keys(StaticCallModuleVod)[8]
        );
        const retBuffer = Buffer.from(CNTVH5PlayerModule.HEAP8.subarray(addr, addr + decRet));

        CNTVH5PlayerModule._jsfree(addr);
        CNTVH5PlayerModule._jsfree(addr2);

        return retBuffer;
    }

    const listFilePath = process.argv[2]; 
    const outputFilePath = process.argv[3]; 

    // 读取并解析 ffconcat 列表文件，获取所有的输入 .264 文件
    const listFileContent = await fsPromises.readFile(listFilePath, 'utf-8');
    const inputFiles = [];
    const lines = listFileContent.split('\n');
    for (const line of lines) {
        const trimmedLine = line.trim();
        if (!trimmedLine || trimmedLine.startsWith('#')) {
            continue;
        }
        if (trimmedLine.startsWith('file ')) {
            // strip 'file ' and any surrounding single quotes
            let filePath = trimmedLine.substring(5).trim();
            if (filePath.startsWith("'") && filePath.endsWith("'")) {
                filePath = filePath.slice(1, -1);
            } else if (filePath.startsWith('"') && filePath.endsWith('"')) {
                filePath = filePath.slice(1, -1);
            }
            
            const resolvedPath = path.isAbsolute(filePath) ? filePath : path.join(path.dirname(listFilePath), filePath);
            inputFiles.push(resolvedPath);
        }
    }
    
    console.log(`[dec.mjs] 从列表文件读取到 ${inputFiles.length} 个输入 H264 文件`);

    // 创建输出流
    const outFile = fs.createWriteStream(outputFilePath);

    // 逐个解密并写入（带进度输出）
    for (let inFileNameIndex = 0; inFileNameIndex < inputFiles.length; inFileNameIndex++) {
        const rawH264FileName = inputFiles[inFileNameIndex];
        const rawH264Buffer = await fsPromises.readFile(rawH264FileName);
        const naluPos = getNaluPos(rawH264Buffer);
        const nalus = naluPos.map(([from, to]) => new NALUnit(rawH264Buffer.subarray(from, to)));

        let shouldDecrypt = false;
        curDate = Date.now().toString();
        InitPlayer();
        
        // 优化：只在需要解密的NAL单元上调用UpdatePlayer
        for (const nalu of nalus) {
            if (nalu.nalUnitType === 25) {
                UpdatePlayer();
                shouldDecrypt = nalu.data[0] === 1;
                const newBuffer = decrypt(Buffer.concat([Buffer.from([nalu.header]), nalu.data]));
                nalu.reload(newBuffer);
            } else if ((nalu.nalUnitType === 1 || nalu.nalUnitType === 5) && shouldDecrypt) {
                UpdatePlayer();
                const newBuffer = decrypt(Buffer.concat([Buffer.from([nalu.header]), nalu.data]));
                nalu.reload(newBuffer);
            }
            // 其他NAL类型：不调用UpdatePlayer，不修改数据
        }
        UnInitPlayer();

        // 将解密后的NAL单元写入输出流
        let currentNALIndex = 0;
        
        function writeNALUs() {
            while (currentNALIndex < nalus.length) {
                // 跳过 Type 25 
                if (nalus[currentNALIndex].nalUnitType === 25) {
                    currentNALIndex++;
                    continue;
                }
                
                const writeOk = outFile.write(nalus[currentNALIndex].dump());
                currentNALIndex++;
                
                if (!writeOk) {
                    return false; // 需要等待 drain
                }
            }
            return true;
        }

        while(currentNALIndex < nalus.length) {
            if (!writeNALUs()) {
                await events.once(outFile, 'drain');
            }
        }
        
        // 输出进度（每10个或最后一个）
        if ((inFileNameIndex + 1) % 10 === 0 || inFileNameIndex === inputFiles.length - 1) {
            reportProgress(inFileNameIndex + 1, inputFiles.length);
        }
    }
    
    outFile.end();
    await events.once(outFile, "finish");
    console.log(`[dec.mjs] 所有文件解密并合并完成，输出至: ${outputFilePath}`);
}

/**
 * 解密单个H264文件
 * @param {Object} CNTVH5PlayerModule - WASM模块实例
 * @param {string} inputPath - 输入文件路径
 * @param {string} outputPath - 输出文件路径
 * @returns {Promise<void>}
 */
async function decryptSingleFile(CNTVH5PlayerModule, inputPath, outputPath) {
    let curDate = Date.now().toString();
    const MemoryExtend = 2048;
    let vmpTag = '';

    function _common(o) {
        const memory = CNTVH5PlayerModule._jsmalloc(curDate.length + MemoryExtend);

        CNTVH5PlayerModule.HEAP8.fill(0, memory, memory + curDate.length + MemoryExtend);
        CNTVH5PlayerModule.HEAP8.set(Array.from(curDate, e => e.charCodeAt(0)), memory);

        let ret;
        switch (o) {
            case "InitPlayer":
                ret = CNTVH5PlayerModule._CNTV_InitPlayer(memory);
                break;
            case "UnInitPlayer":
                ret = CNTVH5PlayerModule._CNTV_UnInitPlayer(memory);
                break;
            case "UpdatePlayer":
                vmpTag = CNTVH5PlayerModule._CNTV_UpdatePlayer(memory).toString(16);
                vmpTag = ['0'.repeat(8 - vmpTag.length), vmpTag].join('');
                ret = 0;
                break;
        }

        CNTVH5PlayerModule._jsfree(memory);
        return ret;
    }

    function InitPlayer() { return _common("InitPlayer"); }
    function UnInitPlayer() { return _common("UnInitPlayer"); }
    function UpdatePlayer() { return _common("UpdatePlayer"); }

    function decrypt(buf) {
        const pageHost = "https://tv.cctv.com";
        const addr = CNTVH5PlayerModule._jsmalloc(buf.length + MemoryExtend);
        const StaticCallModuleVod = {
            H264NalSet: function (e, t, i, n, r) { return e._CNTV_jsdecVOD7(t, i, n, r); },
            H265NalData: function (e, t, i, n, r) { return e._CNTV_jsdecVOD6(t, i, n, r); },
            AVS1AudioKey: function (e, t, i, n, r) { return e._CNTV_jsdecVOD5(t, i, n, r); },
            HEVC2AAC: function (e, t, i, n, r) { return e._CNTV_jsdecVOD4(t, i, n, r); },
            HASHMap: function (e, t, i, n, r) { return e._CNTV_jsdecVOD3(t, i, n, r); },
            BASE64Dec: function (e, t, i, n, r) { return e._CNTV_jsdecVOD2(t, i, n, r); },
            MediaSession: function (e, t, i, n, r) { return e._CNTV_jsdecVOD1(t, i, n, r); },
            Mp4fragment: function (e, t, i, n, r) { return e._CNTV_jsdecVOD0(t, i, n, r); },
            MpegAudio: function (e, t, i, n, r) { return e._CNTV_jsdecVOD8(t, i, n, r); },
            AACDemuxer: function (e, t, i, n, r) { return e._jsdecVOD(i, n, r); }
        };
        function StaticCallModuleVodAPI(e, t, i, n, r, a) {
            return StaticCallModuleVod[a](e, t, i, n, r);
        }

        CNTVH5PlayerModule.HEAP8.set(buf, addr);
        CNTVH5PlayerModule.HEAP8.set(
            Array.from(pageHost, e => e.charCodeAt(0)), addr + buf.length
        );
        const addr2 = CNTVH5PlayerModule._jsmalloc(curDate.length);
        CNTVH5PlayerModule.HEAP8.set(Array.from(curDate, e => e.charCodeAt(0)), addr2);

        for (const i in vmpTag) {
            if ("0123456".includes(vmpTag[i])) {
                StaticCallModuleVodAPI(
                    CNTVH5PlayerModule, addr2, addr, buf.length, pageHost.length, Object.keys(StaticCallModuleVod)[i]
                );
            }
        }

        const decRet = StaticCallModuleVodAPI(
            CNTVH5PlayerModule, addr2, addr, buf.length, pageHost.length, Object.keys(StaticCallModuleVod)[8]
        );
        const retBuffer = Buffer.from(CNTVH5PlayerModule.HEAP8.subarray(addr, addr + decRet));

        CNTVH5PlayerModule._jsfree(addr);
        CNTVH5PlayerModule._jsfree(addr2);

        return retBuffer;
    }

    const rawH264Buffer = await fsPromises.readFile(inputPath);
    const naluPos = getNaluPos(rawH264Buffer);
    const nalus = naluPos.map(([from, to]) => new NALUnit(rawH264Buffer.subarray(from, to)));

    let shouldDecrypt = false;
    curDate = Date.now().toString();
    InitPlayer();
    
    // 优化：只在需要解密的NAL单元上调用UpdatePlayer
    // 预先计算哪些NAL需要解密，减少无效的WASM调用
    for (const nalu of nalus) {
        // Type 25: 加密标记NAL，需要UpdatePlayer + decrypt
        // Type 1/5 + shouldDecrypt: 需要UpdatePlayer + decrypt
        // 其他类型：跳过UpdatePlayer，直接保留原数据
        if (nalu.nalUnitType === 25) {
            UpdatePlayer();
            shouldDecrypt = nalu.data[0] === 1;
            const newBuffer = decrypt(Buffer.concat([Buffer.from([nalu.header]), nalu.data]));
            nalu.reload(newBuffer);
        } else if ((nalu.nalUnitType === 1 || nalu.nalUnitType === 5) && shouldDecrypt) {
            UpdatePlayer();
            const newBuffer = decrypt(Buffer.concat([Buffer.from([nalu.header]), nalu.data]));
            nalu.reload(newBuffer);
        }
        // 其他NAL类型：不调用UpdatePlayer，不修改数据
    }
    UnInitPlayer();

    // 收集解密后的NAL单元（跳过Type 25）
    // 优化：预计算总大小，单次分配Buffer
    let totalSize = 0;
    for (let i = 0; i < nalus.length; i++) {
        if (nalus[i].nalUnitType !== 25) {
            totalSize += nalus[i].start.length + 1 + nalus[i].data.length;
        }
    }
    
    const outputBuffer = Buffer.allocUnsafe(totalSize);
    let offset = 0;
    for (const nalu of nalus) {
        if (nalu.nalUnitType === 25) {
            continue;
        }
        // 直接写入预分配的buffer，避免多次concat
        nalu.start.copy(outputBuffer, offset);
        offset += nalu.start.length;
        outputBuffer[offset++] = nalu.header;
        nalu.data.copy(outputBuffer, offset);
        offset += nalu.data.length;
    }

    // 确保输出目录存在
    const outputDir = path.dirname(outputPath);
    await fsPromises.mkdir(outputDir, { recursive: true });

    // 写入输出文件
    await fsPromises.writeFile(outputPath, outputBuffer);
}

/**
 * 批量映射解密模式（串行处理版本）
 * 注意：多进程并行已在Go端实现，每个Node.js进程只需串行处理自己的任务子集
 * @param {Object} CNTVH5PlayerModule - WASM模块实例
 * @param {string} tasksFilePath - tasks.json文件路径
 */
async function batchMappedDecrypter(CNTVH5PlayerModule, tasksFilePath) {
    // 读取 tasks.json
    const tasksContent = await fsPromises.readFile(tasksFilePath, 'utf-8');
    const tasks = JSON.parse(tasksContent);
    
    console.log(`[dec.mjs] 批量映射模式: 共 ${tasks.length} 个解密任务（串行处理）`);

    // 报告初始进度
    reportProgress(0, tasks.length);

    // 串行处理每个任务（多进程并行由Go端管理）
    for (let i = 0; i < tasks.length; i++) {
        const task = tasks[i];
        const inputPath = task.in;
        const outputPath = task.out;

        // 支持相对路径
        const resolvedInput = path.isAbsolute(inputPath)
            ? inputPath
            : path.join(path.dirname(tasksFilePath), inputPath);
        const resolvedOutput = path.isAbsolute(outputPath)
            ? outputPath
            : path.join(path.dirname(tasksFilePath), outputPath);

        await decryptSingleFile(CNTVH5PlayerModule, resolvedInput, resolvedOutput);
        
        // 报告进度
        reportProgress(i + 1, tasks.length);
    }
    
    console.log(`[dec.mjs] 批量映射解密完成，共处理 ${tasks.length} 个文件`);
}

// 等待Emscripten模块初始化完成的辅助函数
async function waitForModuleInit(module) {
    return new Promise((resolve) => {
        // 检查模块是否已经初始化完成
        // Emscripten模块在初始化完成后会有calledRun=true或__initialized标志
        if (module.calledRun === true || module.__initialized) {
            resolve();
            return;
        }
        
        // 保存原有的回调（如果存在）
        const originalCallback = module.onRuntimeInitialized;
        
        // 设置新的回调
        module.onRuntimeInitialized = () => {
            module.__initialized = true;
            if (originalCallback) {
                originalCallback();
            }
            resolve();
        };
    });
}

async function main() {
    const args = process.argv.slice(2);
    
    // 检查是否为批量映射模式
    if (args[0] === "--batch-mapped") {
        if (args.length < 2) {
            console.error("用法: node dec.mjs --batch-mapped <tasks.json>");
            console.error("tasks.json 格式: [{\"in\": \"seg_0.264\", \"out\": \"seg_0_dec.264\"}, ...]");
            process.exit(1);
        }

        const tasksFilePath = args[1];
        const mjsPath = "./cctv.worker.new.js";
        const CNTVH5PlayerModule = (await import(`${mjsPath}`)).default();
        
        // 等待模块初始化完成
        await waitForModuleInit(CNTVH5PlayerModule);
        
        // 直接调用解密函数
        await batchMappedDecrypter(CNTVH5PlayerModule, tasksFilePath);
        return;
    }

    // 旧版批量模式（向后兼容）
    if (args.length < 2 || args[0] == "--help") {
        console.error("用法:");
        console.error("  批量模式: node dec.mjs <list.txt> <输出的.264文件>");
        console.error("  映射模式: node dec.mjs --batch-mapped <tasks.json>");
        process.exit(1);
    }

    const mjsPath = "./cctv.worker.new.js";
    const CNTVH5PlayerModule = (await import(`${mjsPath}`)).default();
    
    // 等待模块初始化完成
    await waitForModuleInit(CNTVH5PlayerModule);
    
    // 直接调用解密函数
    await mainDecrypter(CNTVH5PlayerModule);
}

main();
