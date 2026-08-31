/* Public broadband endpoints shared by the two broadband test modes. */
(function () {
    'use strict';
    var sharedUpload = [
        'https://mbd.baidu.com/ztbox?action=zpblog',
        'https://vcs.zijieapi.com/vc/setting?aid=6383&pageId=6241'
    ];
    var nodes = [
        { id: '1', label: '综合 CDN 1', category: 'cdn', secure: true, browserUsable: true,
            pingUrls: ['https://lf3-cdn-tos.bytecdntp.com/'],
            downloadUrls: ['https://cdn.aixifan.com/downloads/AcfunLive-Setup-1.9.0.200-ReleaseX64_6d5c40.exe', 'https://devtools.qiniu.com/linux/amd64/qrsctl', 'https://devtools.qiniu.com/qdoractl-darwin-amd64-0.4.6', 'https://gw.alipayobjects.com/os/volans-demo/93211a67-0eed-40ff-8a48-f6c137a88781/MiniProgramStudio-3.1.3.exe', 'https://download.jr.jd.com/downapp/jrapp_jr9631.apk'], uploadUrls: sharedUpload },
        { id: '2', label: '综合 CDN 2', category: 'cdn', secure: true, browserUsable: true,
            pingUrls: ['https://lf3-cdn-tos.bytecdntp.com/'],
            downloadUrls: ['https://downapp.sina.cn/m/06/sinaNews_8.27.0_1719288606_4386_3538_armeabi-v7a.apk', 'https://statics.itc.cn/lt-app/sohumobile_official_gray_optimizeRelease_4_1.0.3_01161850.apk', 'https://lf3-cdn-tos.bytegoofy.com/obj/douyin-pc-client/7044145585217083655/releases/8293088/1.0.8/win32-ia32/douyin-v1.0.8-win32-ia32-douyin.exe', 'https://wwwstatic.vivo.com.cn/vivoportal/files/download/app/20231026/350bda07c8a0719919bcadbf5aea3538.apk', 'https://cd.pddpic.com/android_dev/2024-06-26/06027b4121edcd1f106d992128a7124b.apk'], uploadUrls: sharedUpload }
    ];
    (typeof self !== 'undefined' ? self : window).NetwatchBroadbandNodes = { nodes: nodes, sharedUpload: sharedUpload };
})();
