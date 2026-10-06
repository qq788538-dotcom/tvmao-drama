放置 `sample_3s.mp4`(真实时长约 3 秒的任意 mp4)供 ProbeDurationMs 测试。
文件不入库时测试自动 skip。生成示例:
  ffmpeg -f lavfi -i testsrc=duration=3:size=320x240:rate=30 sample_3s.mp4
