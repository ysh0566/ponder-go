// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract NFT {
    event Transfer(address indexed from, address indexed to, uint256 indexed id);
    constructor() {
        emit Transfer(address(0), address(1), 2**128);
        emit Transfer(address(1), address(2), 2**128);
    }
}

contract MultiToken {
    event TransferSingle(address indexed operator, address indexed from, address indexed to, uint256 id, uint256 amount);
    event TransferBatch(address indexed operator, address indexed from, address indexed to, uint256[] ids, uint256[] amounts);
    constructor() {
        emit TransferSingle(msg.sender, address(0), address(1), 2**128, 100);
        uint256[] memory ids = new uint256[](3);
        uint256[] memory amounts = new uint256[](3);
        ids[0] = 2**128; ids[1] = 2**128; ids[2] = 3;
        amounts[0] = 4; amounts[1] = 5; amounts[2] = 8;
        emit TransferBatch(msg.sender, address(1), address(2), ids, amounts);
    }
}

contract Vault {
    event Transfer(address indexed from, address indexed to, uint256 amount);
    event Approval(address indexed owner, address indexed spender, uint256 amount);
    event Deposit(address indexed caller, address indexed owner, uint256 assets, uint256 shares);
    event Withdraw(address indexed caller, address indexed receiver, address indexed owner, uint256 assets, uint256 shares);
    constructor() {
        emit Transfer(address(0), address(1), 2**128);
        emit Transfer(address(1), address(2), 25);
        emit Approval(address(1), address(2), 70);
        emit Approval(address(1), address(2), 13);
        emit Deposit(address(3), address(1), 150, 100);
        emit Withdraw(address(3), address(2), address(1), 12, 8);
    }
}

contract LlamaChild {
    event ActionCreated(uint256 id, address indexed creator, uint8 role, address indexed strategy,
                        address indexed target, uint256 value, bytes data, string description);
    event Initialized(uint8 version);
    function announce() external {
        emit ActionCreated(1, msg.sender, 2, address(5), address(6), 7, hex"0102", "proposal");
        emit Initialized(1);
    }
}

contract LlamaFactory {
    event LlamaInstanceCreated(address indexed deployer, string indexed name, address llamaCore,
                               address llamaExecutor, address llamaPolicy, uint256 chainId);
    constructor() {
        LlamaChild core = new LlamaChild();
        LlamaChild policy = new LlamaChild();
        LlamaChild outsider = new LlamaChild();
        emit LlamaInstanceCreated(msg.sender, "demo", address(core), address(this), address(policy), block.chainid);
        core.announce();
        policy.announce();
        outsider.announce();
    }
}
