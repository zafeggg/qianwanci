import re, urllib.request, hashlib, pathlib
# 相对本脚本定位仓库根，避免写死盘符（脚本位于 <root>/scripts/）
root = pathlib.Path(__file__).resolve().parent.parent
yml = (root / 'houduan' / 'TiMi' / 'config' / 'etc.local.test.yml').read_text(encoding='utf-8')
key = re.search(r'apiAuthKey:\s*"([^"]+)"', yml).group(1)
print('文件中 key 指纹:', hashlib.md5(key.encode()).hexdigest()[:8])
for label, h in [('无key', {}), ('错key', {'X-API-Key': key + 'x'}), ('对key', {'X-API-Key': key})]:
    req = urllib.request.Request('http://127.0.0.1:3002/api/prices', headers=h)
    print(label, '->', urllib.request.urlopen(req).read().decode()[:60])
