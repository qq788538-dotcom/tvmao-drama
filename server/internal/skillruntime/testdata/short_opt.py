import argparse

p = argparse.ArgumentParser()
p.add_argument("-s", "--source")
a = p.parse_args()
print("SOURCE_CONTENT=" + open(a.source, encoding="utf-8").read())
