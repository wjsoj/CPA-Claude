chars = set()
# GB2312 level-1 (区 16-55) = 3755 most common simplified hanzi.
for qu in range(16, 56):
    for wei in range(1, 95):
        try:
            chars.add(bytes([qu + 0xA0, wei + 0xA0]).decode("gb2312"))
        except UnicodeDecodeError:
            pass
# GB2312 level-2 (区 56-87): rarer hanzi, but names/company names live here.
for qu in range(56, 88):
    for wei in range(1, 95):
        try:
            chars.add(bytes([qu + 0xA0, wei + 0xA0]).decode("gb2312"))
        except UnicodeDecodeError:
            pass
# GB2312 symbol/alphanumeric rows (区 1-9): fullwidth forms, CJK punctuation.
for qu in range(1, 10):
    for wei in range(1, 95):
        try:
            chars.add(bytes([qu + 0xA0, wei + 0xA0]).decode("gb2312"))
        except UnicodeDecodeError:
            pass
# ASCII printable + the currency/typographic marks a statement actually uses.
chars.update(chr(c) for c in range(0x20, 0x7F))
chars.update("¥$€£×÷—–…‘’“”·§№±≈≤≥→←↑↓°‰")
open("charset.txt", "w", encoding="utf-8").write("".join(sorted(chars)))
print("chars:", len(chars))
