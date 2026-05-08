export MGA=/data/eidopsyche/deploy-test/alice
mindgate --state-dir $MGA init --label alice
mindgate --state-dir $MGA relay &
mindgate --state-dir $MGA daemon &

export MGB=/data/eidopsyche/deploy-test/bob
mindgate --state-dir $MGB init --label bob --listen 127.0.0.1:22896
mindgate --state-dir $MGB relay &
mindgate --state-dir $MGB daemon &

# ============ 互加 contact(用 card URI,一行搞定) ============
ACARD=$(mindgate --state-dir $MGA card)
BCARD=$(mindgate --state-dir $MGB card)

mindgate --state-dir $MGA add-contact "$BCARD" --label bob
mindgate --state-dir $MGB add-contact "$ACARD" --label alice

# ============ 发消息 ============
mindgate --state-dir $MGA send bob "hi from alice"

# ============ Bob 收 ============
mindgate --state-dir $MGB inbox --tail